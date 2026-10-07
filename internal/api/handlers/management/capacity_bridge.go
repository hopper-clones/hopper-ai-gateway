package management

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// GetCapacitySnapshot reads the explicitly configured public Capacity owner surface.
// Register only behind management authentication. The actual peer must be local.
func (h *Handler) GetCapacitySnapshot(c *gin.Context) {
	peer, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil || net.ParseIP(peer) == nil || !net.ParseIP(peer).IsLoopback() {
		c.JSON(http.StatusForbidden, gin.H{"code": "CAPACITY_LOCAL_ONLY"})
		return
	}
	script := os.Getenv("HOPPER_AI_CAPACITY_READER")
	if !filepath.IsAbs(script) || os.Getenv("HOPPER_AI_CAPACITY_STATE") == "" || os.Getenv("HOPPER_AI_CAPACITY_PACKAGE") == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "CAPACITY_UNCONFIGURED"})
		return
	}
	bun := os.Getenv("HOPPER_AI_CAPACITY_BUN")
	if bun == "" {
		bun, err = exec.LookPath("bun")
	}
	if err != nil || bun == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "CAPACITY_READER_UNAVAILABLE"})
		return
	}
	cmd := exec.CommandContext(c.Request.Context(), bun, script)
	cmd.Env = append(os.Environ(), "HOPPER_CAPACITY_ACCOUNT_DISCOVERY=off")
	var output, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostic
	if err := cmd.Run(); err != nil {
		code := "CAPACITY_OWNER_UNAVAILABLE"
		var failure struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(diagnostic.Bytes(), &failure) == nil {
			switch failure.Code {
			case "CAPACITY_UNCONFIGURED", "CAPACITY_INVALID_PACKAGE", "CAPACITY_INVALID_SESSION", "CAPACITY_INCOMPLETE_HISTORY":
				code = failure.Code
			}
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": code})
		return
	}
	var result struct {
		SchemaVersion string            `json:"schemaVersion"`
		Accounts      []json.RawMessage `json:"accounts"`
	}
	if json.Unmarshal(output.Bytes(), &result) != nil || result.SchemaVersion != "hopper.gateway-capacity.v1" || result.Accounts == nil {
		c.JSON(http.StatusBadGateway, gin.H{"code": "CAPACITY_INVALID_RESPONSE"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/json", output.Bytes())
}
