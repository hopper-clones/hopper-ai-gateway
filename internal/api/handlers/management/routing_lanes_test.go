package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/capacitycodex"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
)

func routingRequest(t *testing.T, handle gin.HandlerFunc, method, target, body string, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.RemoteAddr = "127.0.0.1:5000"
	c.Params = params
	c.Set(ConfigV8ContextKey, true)
	handle(c)
	return rec
}

func useRoutingClock(t *testing.T, now time.Time) {
	t.Helper()
	previousRouting, previousKeys := routingNow, config.LaneKeyNow
	routingNow = func() time.Time { return now }
	config.LaneKeyNow = func() time.Time { return now }
	t.Cleanup(func() { routingNow, config.LaneKeyNow = previousRouting, previousKeys })
}

func usageAt(id, lane, account string, at time.Time, status string) *feed.UsageEvent {
	event := feed.NewUsageEvent(id, at)
	event.Lane, event.Project, event.Task = lane, "project:search", "task:301"
	event.AccountID, event.Provider, event.Model, event.Status = account, "codex", "gpt-test", status
	event.Tokens = &feed.Tokens{Input: 10, Output: 5, Total: 15}
	event.TokenStatus, event.TokenFields = feed.TokensComplete, []string{"input", "output"}
	return event
}

func quotaAt(id, account, scope string, at, resets time.Time, exhausted bool) *feed.QuotaEvent {
	event := feed.NewQuotaEvent(id, scope, at)
	event.AccountID, event.Provider, event.Exhausted = account, "codex", exhausted
	reset := feed.Time(resets)
	event.ResetsAt = &reset
	return event
}

// routingFixture writes a day of feed: billing-fix moves B -> D after B's weekly
// window is exhausted; pricing-page moves B -> D after B errors; search-index stays on B.
func routingFixture(t *testing.T, now time.Time) (*Handler, *coreauth.Manager) {
	t.Helper()
	store, err := feed.Open(t.TempDir(), feed.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	at := func(hour, minute int) time.Time {
		return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	}
	events := []any{
		usageAt("r1", "billing-fix", "codex-b", at(8, 30), "ok"),
		usageAt("r2", "search-index", "codex-b", at(9, 2), "ok"),
		usageAt("r3", "pricing-page", "codex-b", at(9, 10), "ok"),
		usageAt("r4", "billing-fix", "codex-b", at(13, 30), "ok"),
		quotaAt("r4", "codex-b", "secondary", at(13, 30), at(20, 0), true),
		usageAt("r5", "billing-fix", "codex-d", at(13, 38), "ok"),
		usageAt("r6", "billing-fix", "codex-d", at(13, 40), "ok"),
		usageAt("r0", "old-lane", "codex-a", at(0, 0).Add(-3*time.Hour), "ok"),
	}
	for _, event := range events {
		store.Write(event)
	}
	store.Flush()

	manager := coreauth.NewManager(nil, &coreauth.ResetFirstSelector{}, nil)
	for _, auth := range []*coreauth.Auth{
		{ID: "codex-b", Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{capacitycodex.AttributeAccountLabel: "Codex B"}},
		{ID: "codex-d", Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{capacitycodex.AttributeAccountLabel: "Codex D"}},
	} {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{}
	cfg.Routing.Strategy = "reset-first"
	cfg.LaneKeys = []config.LaneKey{
		{ID: "lk-1", Key: "lk_secret_one", Lane: "search-index", Project: "project:search", Task: "task:301", ExpiresAt: now.Add(2 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "lk-2", Key: "lk_secret_two", Lane: "design-c1", Task: "task:9", ExpiresAt: now.Add(6 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "lk-gone", Key: "lk_secret_gone", Lane: "expired-lane", ExpiresAt: now.Add(-time.Hour).UTC().Format(time.RFC3339)},
	}
	h := &Handler{cfg: cfg, configFilePath: writeTestConfigFile(t), authManager: manager}
	h.SetUsageFeed(store)
	return h, manager
}

type lanesBody struct {
	Strategy string     `json:"strategy"`
	Lanes    []laneView `json:"lanes"`
}

func readLanes(t *testing.T, h *Handler) map[string]laneView {
	t.Helper()
	rec := routingRequest(t, h.GetRoutingLanes, http.MethodGet, "/v8/management/routing/lanes", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("lanes status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "lk_secret") {
		t.Fatalf("lanes leaked a lane key: %s", rec.Body.String())
	}
	var body lanesBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Strategy != "reset-first" {
		t.Fatalf("strategy = %q", body.Strategy)
	}
	byLane := map[string]laneView{}
	for _, lane := range body.Lanes {
		byLane[lane.Lane] = lane
	}
	return byLane
}

func TestRoutingLanesFromKeysAndFeed(t *testing.T) {
	now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.Local)
	useRoutingClock(t, now)
	h, _ := routingFixture(t, now)
	lanes := readLanes(t, h)

	billing := lanes["billing-fix"]
	if billing.Account == nil || billing.Account.AuthID != "codex-d" || billing.Account.Label == nil || *billing.Account.Label != "Codex D" {
		t.Fatalf("billing-fix account = %+v", billing.Account)
	}
	if billing.OnSince == nil || *billing.OnSince != rfc3339(time.Date(2026, 10, 7, 13, 38, 0, 0, time.Local)) {
		t.Fatalf("billing-fix on_since = %v", billing.OnSince)
	}
	if billing.RequestsToday != 4 || billing.TokensToday != 60 || len(billing.ServedToday) != 2 || billing.State != "served" {
		t.Fatalf("billing-fix = %+v", billing)
	}
	if billing.ServedToday[0].Account.AuthID != "codex-b" || billing.ServedToday[0].Requests != 2 {
		t.Fatalf("billing-fix served_today = %+v", billing.ServedToday)
	}
	search := lanes["search-index"]
	if len(search.Keys) != 1 || search.Keys[0].KeyID != feed.KeyID("lk_secret_one") || search.Pin != nil {
		t.Fatalf("search-index = %+v", search)
	}
	if design := lanes["design-c1"]; design.State != "idle" || design.Account != nil || len(design.Keys) != 1 {
		t.Fatalf("design-c1 (key, no request) = %+v", design)
	}
	if old := lanes["old-lane"]; old.Account == nil || old.RequestsToday != 0 || len(old.ServedToday) != 0 {
		t.Fatalf("old-lane (served yesterday) = %+v", old)
	}
	if _, ok := lanes["expired-lane"]; ok {
		t.Fatal("an expired key must not make a lane")
	}
}

func TestRoutingSwapsJournal(t *testing.T) {
	now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.Local)
	useRoutingClock(t, now)
	h, _ := routingFixture(t, now)
	since := time.Date(2026, 10, 7, 0, 0, 0, 0, time.Local)
	rec := routingRequest(t, h.GetRoutingSwaps, http.MethodGet, "/v8/management/routing/swaps?since="+since.UTC().Format(time.RFC3339), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("swaps status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Swaps []swapView `json:"swaps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Swaps) != 1 {
		t.Fatalf("swaps = %+v", body.Swaps)
	}
	swap := body.Swaps[0]
	if swap.Lane != "billing-fix" || swap.From.AuthID != "codex-b" || swap.To.AuthID != "codex-d" || swap.From.Label == nil || *swap.From.Label != "Codex B" {
		t.Fatalf("swap = %+v", swap)
	}
	if swap.Reason == nil || *swap.Reason != swapReasonExhausted || swap.Scope == nil || *swap.Scope != "secondary" || swap.ResetsAt == nil {
		t.Fatalf("swap reason = %v scope = %v resets = %v", swap.Reason, swap.Scope, swap.ResetsAt)
	}
	if rec := routingRequest(t, h.GetRoutingSwaps, http.MethodGet, "/v8/management/routing/swaps?since=yesterday", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad since status = %d", rec.Code)
	}
}

func TestSwapReasonsUnavailableResetFirstAndPinned(t *testing.T) {
	base := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	line := func(lane, account, status string, minutes int) feedLine {
		return feedLine{Kind: feed.KindUsage, At: feed.Time(base.Add(time.Duration(minutes) * time.Minute)), Lane: lane, AccountID: account, Status: status}
	}
	routing := config.RoutingConfig{Strategy: "reset-first", LanePins: []config.LanePin{{Lane: "c", AuthID: "acct-2", PinnedAt: base.Add(25 * time.Minute).Format(time.RFC3339)}}}
	history := deriveRoutingHistory([]feedLine{
		line("a", "acct-1", "error", 0), line("a", "acct-2", "ok", 1),
		line("b", "acct-3", "ok", 10), line("b", "acct-4", "ok", 11),
		line("c", "acct-3", "ok", 20), line("c", "acct-2", "ok", 30),
	}, base, routing)
	want := []string{swapReasonUnavailable, swapReasonResetFirst, swapReasonPinned}
	if len(history.swaps) != len(want) {
		t.Fatalf("swaps = %+v", history.swaps)
	}
	for i, reason := range want {
		if history.swaps[i].reason != reason {
			t.Fatalf("swap %d reason = %q, want %q", i, history.swaps[i].reason, reason)
		}
	}
	plain := deriveRoutingHistory([]feedLine{line("b", "acct-3", "ok", 10), line("b", "acct-4", "ok", 11)}, base, config.RoutingConfig{})
	if len(plain.swaps) != 1 || plain.swaps[0].reason != "" {
		t.Fatalf("round-robin swap reason must be unknown, got %+v", plain.swaps)
	}
}

func TestLanePinPutDeleteAndPinnedOut(t *testing.T) {
	now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.Local)
	useRoutingClock(t, now)
	h, manager := routingFixture(t, now)

	put := func(lane, body string) *httptest.ResponseRecorder {
		return routingRequest(t, h.PutLanePin, http.MethodPut, "/v8/management/routing/lanes/"+lane+"/pin", body, gin.Params{{Key: "lane", Value: lane}})
	}
	if rec := put("search-index", `{"account":"nobody"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("pin to unknown account status = %d", rec.Code)
	}
	if rec := put("search-index", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("pin without account status = %d", rec.Code)
	}
	if rec := put("search-index", `{"account":"codex-b"}`); rec.Code != http.StatusOK {
		t.Fatalf("pin status = %d body=%s", rec.Code, rec.Body.String())
	}
	saved, err := config.LoadConfig(h.configFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if pin, ok := saved.Routing.LanePinFor("search-index"); !ok || pin.AuthID != "codex-b" || pin.PinnedAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("persisted pins = %+v", saved.Routing.LanePins)
	}
	search := readLanes(t, h)["search-index"]
	if search.Pin == nil || search.Pin.Out || search.Pin.Account.AuthID != "codex-b" || search.State != "served" {
		t.Fatalf("pinned lane = %+v pin=%+v", search, search.Pin)
	}

	// Codex B's weekly window is exhausted until 20:00: the lane is pinned but out.
	reset := time.Date(2026, 10, 7, 20, 0, 0, 0, time.Local)
	auth, _ := manager.GetByID("codex-b")
	auth.Quota = coreauth.QuotaState{ObservedAt: now.Add(-time.Minute), Signals: map[string]string{
		"x-codex-secondary-used-percent": "100",
		"x-codex-secondary-reset-at":     reset.UTC().Format(time.RFC3339),
	}}
	if _, err := manager.Update(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	search = readLanes(t, h)["search-index"]
	if search.State != "pinned-out" || search.Pin == nil || !search.Pin.Out || search.Pin.OutUntil == nil {
		t.Fatalf("pinned-out lane = %+v pin=%+v", search, search.Pin)
	}

	del := func(lane string) *httptest.ResponseRecorder {
		return routingRequest(t, h.DeleteLanePin, http.MethodDelete, "/v8/management/routing/lanes/"+lane+"/pin", "", gin.Params{{Key: "lane", Value: lane}})
	}
	if rec := del("search-index"); rec.Code != http.StatusOK {
		t.Fatalf("unpin status = %d", rec.Code)
	}
	if rec := del("search-index"); rec.Code != http.StatusNotFound {
		t.Fatalf("second unpin status = %d", rec.Code)
	}
	saved, err = config.LoadConfig(h.configFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Routing.LanePins) != 0 {
		t.Fatalf("pins after unpin = %+v", saved.Routing.LanePins)
	}
}

func TestRoutingReadsRequireLoopbackAndFeed(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	for _, handle := range []gin.HandlerFunc{h.GetRoutingLanes, h.GetRoutingSwaps} {
		if rec := routingRequest(t, handle, http.MethodGet, "/v8/management/routing/lanes", "", nil); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("without feed status = %d", rec.Code)
		}
	}
}

func TestRoutingLaneReportsIncompleteAttemptCoverage(t *testing.T) {
	now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.Local)
	useRoutingClock(t, now)
	h, _ := routingFixture(t, now)
	for _, status := range []string{feed.TokensComplete, feed.TokensPartial, feed.TokensUnavailable, feed.TokensInvalid} {
		event := usageAt("coverage-"+status, "coverage", "codex-b", now.Add(-time.Minute), "error")
		event.TokenStatus = status
		switch status {
		case feed.TokensComplete:
			event.Tokens = &feed.Tokens{}
		case feed.TokensPartial:
			event.Tokens = &feed.Tokens{Input: 8, Total: 8}
			event.TokenFields = []string{"input"}
		default:
			event.Tokens = nil
			event.TokenFields = []string{}
		}
		h.usageFeed.Write(event)
	}
	h.usageFeed.Flush()
	lane := readLanes(t, h)["coverage"]
	if lane.RequestsToday != 4 || lane.TokensToday != 8 || lane.PartialUsageToday != 1 || lane.UnavailableUsageToday != 1 || lane.InvalidUsageToday != 1 || lane.State != "failing" {
		t.Fatalf("coverage lost in public lane read: %+v", lane)
	}
}
