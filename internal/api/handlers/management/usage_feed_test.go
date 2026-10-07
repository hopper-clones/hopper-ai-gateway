package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
)

func feedRequest(t *testing.T, h *Handler, method, target, body, peer string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.RemoteAddr = peer
	switch {
	case strings.HasSuffix(target, "/ack"):
		h.AckUsageFeed(c)
	case strings.HasSuffix(target, "/cursors"):
		h.GetUsageFeedCursors(c)
	default:
		h.GetUsageFeed(c)
	}
	return rec
}

func TestUsageFeedEndpointsRequireLoopbackAndStore(t *testing.T) {
	h := &Handler{}
	for _, target := range []string{"/v8/management/usage/feed", "/v8/management/usage/feed/ack", "/v8/management/usage/feed/cursors"} {
		if rec := feedRequest(t, h, http.MethodGet, target, "", "203.0.113.7:1234"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "USAGE_FEED_LOCAL_ONLY") {
			t.Fatalf("%s remote peer: status=%d body=%s", target, rec.Code, rec.Body.String())
		}
		if rec := feedRequest(t, h, http.MethodGet, target, "", "127.0.0.1:1234"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "USAGE_FEED_DISABLED") {
			t.Fatalf("%s without store: status=%d body=%s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestUsageFeedReadAckAndCursors(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	store, err := feed.Open(t.TempDir(), feed.WithClock(func() time.Time { return at }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"a", "b", "c"} {
		store.Write(feed.NewUsageEvent(id, at))
	}
	store.Flush()
	h := &Handler{}
	h.SetUsageFeed(store)

	rec := feedRequest(t, h, http.MethodGet, "/v8/management/usage/feed?limit=2", "", "[::1]:4321")
	if rec.Code != http.StatusOK {
		t.Fatalf("read status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("feed responses must not be cached")
	}
	var page feed.Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page = %+v", page)
	}

	rec = feedRequest(t, h, http.MethodPost, "/v8/management/usage/feed/ack", `{"consumer":"capacity","cursor":"`+page.NextCursor+`"}`, "127.0.0.1:4321")
	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = feedRequest(t, h, http.MethodGet, "/v8/management/usage/feed/cursors", "", "127.0.0.1:4321")
	if rec.Code != http.StatusOK {
		t.Fatalf("cursors status = %d", rec.Code)
	}
	var cursors struct {
		Cursors map[string]feed.AckedCursor `json:"cursors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cursors); err != nil {
		t.Fatal(err)
	}
	if cursors.Cursors["capacity"].Cursor != page.NextCursor {
		t.Fatalf("cursors = %+v", cursors)
	}

	rec = feedRequest(t, h, http.MethodGet, "/v8/management/usage/feed?cursor="+page.NextCursor, "", "127.0.0.1:4321")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.HasMore {
		t.Fatalf("resumed page = %+v", page)
	}

	for _, target := range []string{"/v8/management/usage/feed?cursor=bad", "/v8/management/usage/feed?limit=x"} {
		if rec := feedRequest(t, h, http.MethodGet, target, "", "127.0.0.1:4321"); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", target, rec.Code)
		}
	}
	if rec := feedRequest(t, h, http.MethodPost, "/v8/management/usage/feed/ack", `{"consumer":"","cursor":"bad"}`, "127.0.0.1:4321"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad ack status = %d", rec.Code)
	}
}
