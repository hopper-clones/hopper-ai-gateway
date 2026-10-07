package management

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestCapacityBridgeUsesActualLoopbackPeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		peer string
		want int
	}{{"203.0.113.7:1234", 403}, {"127.0.0.1:1234", 503}, {"[::1]:1234", 503}, {"", 403}} {
		t.Run(tc.peer, func(t *testing.T) {
			t.Setenv("HOPPER_AI_CAPACITY_READER", "")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/capacity/snapshot", nil)
			c.Request.RemoteAddr = tc.peer
			c.Request.Header.Set("X-Forwarded-For", "127.0.0.1")
			(&Handler{}).GetCapacitySnapshot(c)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
}
