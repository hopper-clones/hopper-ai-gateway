package management

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
)

const (
	defaultLaneKeyTTL = 8 * time.Hour
	maxLaneKeyTTL     = 30 * 24 * time.Hour
)

type laneKeyView struct {
	ID        string `json:"id"`
	KeyID     string `json:"key_id"`
	Lane      string `json:"lane"`
	Project   string `json:"project"`
	Task      string `json:"task"`
	ExpiresAt string `json:"expires_at"`
}

func laneKeyViewOf(key config.LaneKey) laneKeyView {
	return laneKeyView{ID: key.ID, KeyID: feed.KeyID(key.Key), Lane: key.Lane, Project: key.Project, Task: key.Task, ExpiresAt: key.ExpiresAt}
}

// CreateLaneKey serves POST /lane-keys {lane, project, task, ttl_seconds}.
// The plaintext key is returned once and persisted so auth can verify it.
func (h *Handler) CreateLaneKey(c *gin.Context) {
	var body struct {
		Lane       string `json:"lane"`
		Project    string `json:"project"`
		Task       string `json:"task"`
		TTLSeconds int64  `json:"ttl_seconds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	body.Lane = strings.TrimSpace(body.Lane)
	if body.Lane == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lane is required"})
		return
	}
	ttl := defaultLaneKeyTTL
	if body.TTLSeconds != 0 {
		ttl = time.Duration(body.TTLSeconds) * time.Second
	}
	if ttl <= 0 || ttl > maxLaneKeyTTL {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ttl_seconds must be between 1 and 2592000"})
		return
	}
	id, secret, err := newLaneKeyMaterial()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate lane key"})
		return
	}
	now := config.LaneKeyNow()
	key := config.LaneKey{
		ID:        id,
		Key:       secret,
		Lane:      body.Lane,
		Project:   strings.TrimSpace(body.Project),
		Task:      strings.TrimSpace(body.Task),
		ExpiresAt: now.Add(ttl).UTC().Format(time.RFC3339),
	}

	h.mu.Lock()
	h.cfg.PruneExpiredLaneKeys(now)
	h.cfg.LaneKeys = append(h.cfg.LaneKeys, key)
	snapshot, saved := h.saveConfigAndSnapshotLocked(c)
	h.mu.Unlock()
	if !saved {
		return
	}
	// The key must authenticate the moment the caller receives it.
	h.reloadConfigAfterManagementSave(c.Request.Context(), snapshot)
	c.JSON(http.StatusCreated, gin.H{
		"id": key.ID, "key": key.Key, "expires_at": key.ExpiresAt,
		"lane": key.Lane, "project": key.Project, "task": key.Task,
	})
}

// ListLaneKeys serves GET /lane-keys. It never returns the keys themselves.
func (h *Handler) ListLaneKeys(c *gin.Context) {
	now := config.LaneKeyNow()
	h.mu.Lock()
	views := make([]laneKeyView, 0, len(h.cfg.LaneKeys))
	for _, key := range h.cfg.LaneKeys {
		if !key.Expired(now) {
			views = append(views, laneKeyViewOf(key))
		}
	}
	h.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"lane_keys": views})
}

// DeleteLaneKey serves DELETE /lane-keys/:id.
func (h *Handler) DeleteLaneKey(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	h.mu.Lock()
	kept := make([]config.LaneKey, 0, len(h.cfg.LaneKeys))
	for _, key := range h.cfg.LaneKeys {
		if key.ID != id {
			kept = append(kept, key)
		}
	}
	if len(kept) == len(h.cfg.LaneKeys) {
		h.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "lane key not found"})
		return
	}
	if len(kept) == 0 {
		kept = nil
	}
	h.cfg.LaneKeys = kept
	h.cfg.PruneExpiredLaneKeys(config.LaneKeyNow())
	snapshot, saved := h.saveConfigAndSnapshotLocked(c)
	h.mu.Unlock()
	if !saved {
		return
	}
	// A revoked key must stop authenticating before the caller is told it is gone.
	h.reloadConfigAfterManagementSave(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": id})
}

// newLaneKeyMaterial returns a short id and a 48-hex-character secret.
func newLaneKeyMaterial() (id, secret string, err error) {
	raw := make([]byte, 6+24)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	return "lk-" + hex.EncodeToString(raw[:6]), "lk_" + hex.EncodeToString(raw[6:]), nil
}
