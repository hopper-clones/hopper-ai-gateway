package management

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

// PutLanePin serves PUT /routing/lanes/:lane/pin {account}: the lane prefers
// that gateway credential. When it cannot serve a request, selection falls back.
func (h *Handler) PutLanePin(c *gin.Context) {
	lane := strings.TrimSpace(c.Param("lane"))
	if lane == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "ROUTING_BAD_LANE", "error": "lane is required"})
		return
	}
	var body struct {
		Account string `json:"account"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Account) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "ROUTING_BAD_PIN", "error": "account is required"})
		return
	}
	account := strings.TrimSpace(body.Account)
	if h.authManager != nil {
		if auth, ok := h.authManager.GetByID(account); !ok || auth == nil {
			c.JSON(http.StatusNotFound, gin.H{"code": "ROUTING_UNKNOWN_ACCOUNT", "error": "no credential with that id"})
			return
		}
	}
	pin := config.LanePin{Lane: lane, AuthID: account, PinnedAt: routingNow().UTC().Format(time.RFC3339)}
	h.mu.Lock()
	h.cfg.Routing.LanePins = config.WithLanePin(h.cfg.Routing.LanePins, pin)
	snapshot, saved := h.saveConfigAndSnapshotLocked(c)
	h.mu.Unlock()
	if !saved {
		return
	}
	// The pin must steer the lane's next request.
	h.reloadConfigAfterManagementSave(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "lane": lane, "account": h.accountView(account, nil, ""), "pinned_at": pin.PinnedAt})
}

// DeleteLanePin serves DELETE /routing/lanes/:lane/pin: the lane goes back to the configured strategy.
func (h *Handler) DeleteLanePin(c *gin.Context) {
	lane := strings.TrimSpace(c.Param("lane"))
	h.mu.Lock()
	kept, removed := config.WithoutLanePin(h.cfg.Routing.LanePins, lane)
	if !removed {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"code": "ROUTING_NOT_PINNED", "error": "the lane is not pinned"})
		return
	}
	h.cfg.Routing.LanePins = kept
	snapshot, saved := h.saveConfigAndSnapshotLocked(c)
	h.mu.Unlock()
	if !saved {
		return
	}
	h.reloadConfigAfterManagementSave(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "lane": lane})
}
