package helps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestNewUsageReporterCapturesLaneIdentityAtConstruction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ginCtx.Set("userApiKey", "lane-secret")
	ginCtx.Set("accessMetadata", map[string]string{"lane": "lane-a", "project": "project:1", "task": "task:1"})
	ctx := context.WithValue(context.Background(), "gin", ginCtx)

	reporter := NewUsageReporter(ctx, "claude", "claude-fable-5-1", nil)
	// The gin context is recycled by the time the usage manager dispatches.
	ginCtx.Set("accessMetadata", map[string]string{"lane": "someone-else"})
	ginCtx.Set("userApiKey", "")

	record := reporter.buildRecord(usage.Detail{}, false)
	if record.Lane != "lane-a" || record.Project != "project:1" || record.Task != "task:1" || record.APIKey != "lane-secret" {
		t.Fatalf("record identity = lane=%q project=%q task=%q key=%q", record.Lane, record.Project, record.Task, record.APIKey)
	}
	lane, project, task := LaneIdentityFromContext(context.Background())
	if lane != "" || project != "" || task != "" {
		t.Fatal("no gin context must give empty identity")
	}
}
