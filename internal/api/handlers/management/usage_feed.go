package management

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
	log "github.com/sirupsen/logrus"
)

// SetUsageFeed attaches the feed store served by the usage feed endpoints.
func (h *Handler) SetUsageFeed(store *feed.Store) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.usageFeed = store
	h.mu.Unlock()
}

// usageFeedStore gates the feed endpoints: loopback peer and an enabled feed.
func (h *Handler) usageFeedStore(c *gin.Context) (*feed.Store, bool) {
	if !loopbackPeer(c) {
		c.JSON(http.StatusForbidden, gin.H{"code": "USAGE_FEED_LOCAL_ONLY"})
		return nil, false
	}
	if h == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "USAGE_FEED_DISABLED"})
		return nil, false
	}
	h.mu.Lock()
	store := h.usageFeed
	h.mu.Unlock()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "USAGE_FEED_DISABLED"})
		return nil, false
	}
	return store, true
}

// GetUsageFeed serves GET /usage/feed?cursor=&limit=.
func (h *Handler) GetUsageFeed(c *gin.Context) {
	store, ok := h.usageFeedStore(c)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "USAGE_FEED_BAD_LIMIT"})
			return
		}
		limit = parsed
	}
	cursor := strings.TrimSpace(c.Query("cursor"))
	if cursor != "" {
		if _, _, err := feed.ParseCursor(cursor); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "USAGE_FEED_BAD_CURSOR"})
			return
		}
	}
	page, err := store.Read(cursor, limit)
	if err != nil {
		log.WithError(err).Error("usage feed: read")
		c.JSON(http.StatusInternalServerError, gin.H{"code": "USAGE_FEED_READ_FAILED"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, page)
}

// AckUsageFeed serves POST /usage/feed/ack {consumer, cursor}.
func (h *Handler) AckUsageFeed(c *gin.Context) {
	store, ok := h.usageFeedStore(c)
	if !ok {
		return
	}
	var body struct {
		Consumer string `json:"consumer"`
		Cursor   string `json:"cursor"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "USAGE_FEED_BAD_ACK", "error": "invalid body"})
		return
	}
	if err := store.Ack(strings.TrimSpace(body.Consumer), strings.TrimSpace(body.Cursor)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "USAGE_FEED_BAD_ACK", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "consumer": strings.TrimSpace(body.Consumer), "cursor": strings.TrimSpace(body.Cursor)})
}

// GetUsageFeedCursors serves GET /usage/feed/cursors.
func (h *Handler) GetUsageFeedCursors(c *gin.Context) {
	store, ok := h.usageFeedStore(c)
	if !ok {
		return
	}
	cursors, err := store.Cursors()
	if err != nil {
		log.WithError(err).Error("usage feed: list cursors")
		c.JSON(http.StatusInternalServerError, gin.H{"code": "USAGE_FEED_READ_FAILED"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"cursors": cursors})
}
