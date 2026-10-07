package management

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
)

type pickWindow struct {
	Scope     string  `json:"scope"`
	ResetsAt  *string `json:"resets_at"`
	Exhausted bool    `json:"exhausted"`
}

// PostRoutingPick serves POST /routing/pick {model, lane}. It runs the manager's
// read-only selection path (PeekAuth) for the model: nothing is executed and no
// rotation, credit or session state moves. Lane is echoed for the caller's
// trace; selection itself is lane-agnostic.
func (h *Handler) PostRoutingPick(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	var body struct {
		Model string `json:"model"`
		Lane  string `json:"lane"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	body.Model = strings.TrimSpace(body.Model)
	if body.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	providers := util.GetProviderName(body.Model)
	if len(providers) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no provider serves model " + body.Model})
		return
	}
	var (
		selected *coreauth.Auth
		provider string
		lastErr  error
	)
	for _, candidate := range providers {
		auth, err := h.authManager.PeekAuth(c.Request.Context(), candidate, body.Model, cliproxyexecutor.Options{})
		if err == nil && auth != nil {
			selected, provider = auth, candidate
			break
		}
		if err != nil {
			lastErr = err
		}
	}
	if selected == nil {
		message := "no credential available"
		if lastErr != nil {
			message = lastErr.Error()
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": message})
		return
	}
	now := time.Now()
	windows := make([]pickWindow, 0, 4)
	reason := "stable-order"
	for _, window := range coreauth.RoutingQuotaWindows(selected, body.Model, now) {
		view := pickWindow{Scope: window.Scope, Exhausted: window.Exhausted}
		if !window.ResetAt.IsZero() {
			resetsAt := window.ResetAt.UTC().Format(time.RFC3339)
			view.ResetsAt = &resetsAt
			if !window.Stale && !window.Exhausted && window.ResetAt.After(now) {
				reason = "earliest-reset"
			}
		}
		windows = append(windows, view)
	}
	var accountHash *string
	if kind, value := selected.AccountInfo(); kind == "oauth" {
		accountHash = feed.AccountHash(value)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"auth_id":      selected.ID,
		"account_hash": accountHash,
		"provider":     provider,
		"model":        body.Model,
		"lane":         strings.TrimSpace(body.Lane),
		"reason":       reason,
		"windows":      windows,
	})
}
