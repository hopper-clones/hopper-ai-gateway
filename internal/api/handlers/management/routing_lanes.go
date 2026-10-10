package management

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/capacitycodex"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
	log "github.com/sirupsen/logrus"
)

// routingNow is the clock the routing reads and pins use; tests replace it.
var routingNow = time.Now

const (
	// laneLookback is how far back a lane's current account is looked for.
	laneLookback = 48 * time.Hour
	// swapLookback is the default window of the swap journal.
	swapLookback = 7 * 24 * time.Hour
	// swapStateLead is read before `since` so the first swap after it is seen.
	swapStateLead = 6 * time.Hour
)

type routingAccountView struct {
	AuthID      string  `json:"auth_id"`
	AccountHash *string `json:"account_hash"`
	Label       *string `json:"label"`
	Provider    string  `json:"provider,omitempty"`
}

type laneKeyRef struct {
	ID        string `json:"id"`
	KeyID     string `json:"key_id"`
	ExpiresAt string `json:"expires_at"`
}

type laneRunView struct {
	Account  routingAccountView `json:"account"`
	From     string             `json:"from"`
	To       string             `json:"to"`
	Requests int                `json:"requests"`
}

type lanePinView struct {
	Account  routingAccountView `json:"account"`
	PinnedAt *string            `json:"pinned_at"`
	Out      bool               `json:"out"`
	OutWhy   *string            `json:"out_why"`
	OutUntil *string            `json:"out_until"`
}

type laneView struct {
	Lane                  string              `json:"lane"`
	Project               string              `json:"project"`
	Task                  string              `json:"task"`
	State                 string              `json:"state"`
	Account               *routingAccountView `json:"account"`
	OnSince               *string             `json:"on_since"`
	LastRequestAt         *string             `json:"last_request_at"`
	LastStatus            *string             `json:"last_status"`
	RequestsToday         int                 `json:"requests_today"`
	PartialUsageToday     int                 `json:"partial_usage_today"`
	UnavailableUsageToday int                 `json:"unavailable_usage_today"`
	InvalidUsageToday     int                 `json:"invalid_usage_today"`
	TokensToday           int64               `json:"tokens_today"`
	ServedToday           []laneRunView       `json:"served_today"`
	Pin                   *lanePinView        `json:"pin"`
	Keys                  []laneKeyRef        `json:"keys"`
}

type swapView struct {
	At       string             `json:"at"`
	Lane     string             `json:"lane"`
	Project  string             `json:"project"`
	Task     string             `json:"task"`
	From     routingAccountView `json:"from"`
	To       routingAccountView `json:"to"`
	Reason   *string            `json:"reason"`
	Scope    *string            `json:"scope"`
	ResetsAt *string            `json:"resets_at"`
}

func rfc3339(at time.Time) string { return at.UTC().Format(time.RFC3339) }

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// routingSince reads `since` (RFC 3339) or falls back to now minus lookback.
func routingSince(c *gin.Context, now time.Time, lookback time.Duration) (time.Time, bool) {
	raw := strings.TrimSpace(c.Query("since"))
	if raw == "" {
		return now.Add(-lookback), true
	}
	since, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "ROUTING_BAD_SINCE", "error": "since must be an RFC 3339 time"})
		return time.Time{}, false
	}
	return since, true
}

// accountView names an account by its gateway id, its hashed identity and, when
// Capacity registered it, Capacity's label. No email leaves the gateway.
func (h *Handler) accountView(authID string, hash *string, provider string) routingAccountView {
	view := routingAccountView{AuthID: authID, AccountHash: hash, Provider: provider}
	if h.authManager == nil || authID == "" {
		return view
	}
	auth, ok := h.authManager.GetByID(authID)
	if !ok || auth == nil {
		return view
	}
	if label := strings.TrimSpace(auth.Attributes[capacitycodex.AttributeAccountLabel]); label != "" {
		view.Label = &label
	}
	if view.AccountHash == nil {
		if kind, value := auth.AccountInfo(); kind == "oauth" {
			view.AccountHash = feed.AccountHash(value)
		}
	}
	if view.Provider == "" {
		view.Provider = auth.Provider
	}
	return view
}

// authOut reports whether a credential cannot serve model now, why, and until when.
func (h *Handler) authOut(authID, model string, now time.Time) (bool, string, *time.Time) {
	if h.authManager == nil {
		return false, "", nil
	}
	auth, ok := h.authManager.GetByID(authID)
	if !ok || auth == nil {
		return true, "missing", nil
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return true, "disabled", nil
	}
	var until *time.Time
	for _, window := range coreauth.RoutingQuotaWindows(auth, model, now) {
		if window.Exhausted && !window.Stale && window.ResetAt.After(now) && (until == nil || window.ResetAt.After(*until)) {
			reset := window.ResetAt
			until = &reset
		}
	}
	if until != nil {
		return true, swapReasonExhausted, until
	}
	if auth.Unavailable && auth.NextRetryAfter.After(now) {
		retry := auth.NextRetryAfter
		return true, swapReasonUnavailable, &retry
	}
	if state := auth.ModelStates[model]; model != "" && state != nil && state.Unavailable && state.NextRetryAfter.After(now) {
		retry := state.NextRetryAfter
		return true, swapReasonUnavailable, &retry
	}
	return false, "", nil
}

// routingSnapshot copies what routing reads from the config under the lock.
func (h *Handler) routingSnapshot(now time.Time) (config.RoutingConfig, []config.LaneKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		return config.RoutingConfig{}, nil
	}
	routing := h.cfg.Routing
	routing.LanePins = append([]config.LanePin(nil), h.cfg.Routing.LanePins...)
	keys := make([]config.LaneKey, 0, len(h.cfg.LaneKeys))
	for _, key := range h.cfg.LaneKeys {
		if !key.Expired(now) {
			keys = append(keys, key)
		}
	}
	return routing, keys
}

// GetRoutingLanes serves GET /routing/lanes: per lane, the account serving it
// now, since when, today's requests and accounts, its pin and its keys.
func (h *Handler) GetRoutingLanes(c *gin.Context) {
	store, ok := h.usageFeedStore(c)
	if !ok {
		return
	}
	now := routingNow()
	from, ok := routingSince(c, now, laneLookback)
	if !ok {
		return
	}
	routing, keys := h.routingSnapshot(now)
	lines, truncated, err := readRoutingLines(store, from)
	if err != nil {
		log.WithError(err).Error("routing lanes: read feed")
		c.JSON(http.StatusInternalServerError, gin.H{"code": "USAGE_FEED_READ_FAILED"})
		return
	}
	local := now.Local()
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	history := deriveRoutingHistory(lines, dayStart, routing)

	views := map[string]*laneView{}
	viewOf := func(lane string) *laneView {
		if views[lane] == nil {
			views[lane] = &laneView{Lane: lane, ServedToday: []laneRunView{}, Keys: []laneKeyRef{}}
		}
		return views[lane]
	}
	for _, key := range keys {
		view := viewOf(strings.TrimSpace(key.Lane))
		view.Keys = append(view.Keys, laneKeyRef{ID: key.ID, KeyID: feed.KeyID(key.Key), ExpiresAt: key.ExpiresAt})
		view.Project, view.Task = key.Project, key.Task
	}
	for lane, entry := range history.lanes {
		h.fillLaneFromHistory(viewOf(lane), entry, dayStart)
	}
	for _, pin := range routing.LanePins {
		viewOf(strings.TrimSpace(pin.Lane))
	}
	lanes := make([]laneView, 0, len(views))
	for lane, view := range views {
		if lane == "" {
			continue
		}
		h.fillLanePin(view, routing, history.lanes[lane], now)
		view.State = laneState(view)
		lanes = append(lanes, *view)
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].Lane < lanes[j].Lane })
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"read_at":   rfc3339(now),
		"since":     rfc3339(from),
		"today":     rfc3339(dayStart),
		"strategy":  strings.TrimSpace(routing.Strategy),
		"truncated": truncated,
		"lanes":     lanes,
	})
}

func (h *Handler) fillLaneFromHistory(view *laneView, entry *laneHistory, dayStart time.Time) {
	if view.Project == "" {
		view.Project = entry.project
	}
	if view.Task == "" {
		view.Task = entry.task
	}
	current := entry.runs[len(entry.runs)-1]
	account := h.accountView(current.account.AccountID, current.account.AccountHash, current.account.Provider)
	view.Account = &account
	view.OnSince = stringPtr(rfc3339(current.from))
	view.LastRequestAt = stringPtr(rfc3339(entry.last.at()))
	view.LastStatus = stringPtr(entry.last.Status)
	view.RequestsToday, view.TokensToday = entry.requests, entry.tokens
	view.PartialUsageToday, view.UnavailableUsageToday, view.InvalidUsageToday = entry.partial, entry.unavailable, entry.invalid
	for _, run := range entry.runs {
		if run.to.Before(dayStart) {
			continue
		}
		view.ServedToday = append(view.ServedToday, laneRunView{
			Account:  h.accountView(run.account.AccountID, run.account.AccountHash, run.account.Provider),
			From:     rfc3339(run.from),
			To:       rfc3339(run.to),
			Requests: run.requests,
		})
	}
}

func (h *Handler) fillLanePin(view *laneView, routing config.RoutingConfig, entry *laneHistory, now time.Time) {
	pin, ok := routing.LanePinFor(view.Lane)
	if !ok {
		return
	}
	model := ""
	if entry != nil {
		model = entry.last.Model
	}
	out, why, until := h.authOut(pin.AuthID, model, now)
	pinView := &lanePinView{Account: h.accountView(pin.AuthID, nil, ""), PinnedAt: stringPtr(pin.PinnedAt), Out: out, OutWhy: stringPtr(why)}
	if until != nil {
		pinView.OutUntil = stringPtr(rfc3339(*until))
	}
	view.Pin = pinView
}

// laneState is one word: served, pinned-out, failing or idle.
func laneState(view *laneView) string {
	switch {
	case view.Pin != nil && view.Pin.Out:
		return "pinned-out"
	case view.LastRequestAt == nil:
		return "idle"
	case view.LastStatus != nil && *view.LastStatus == "error":
		return "failing"
	default:
		return "served"
	}
}

// GetRoutingSwaps serves GET /routing/swaps?since=: every move of a lane from
// one account to another, newest first, with the reason when the feed shows it.
func (h *Handler) GetRoutingSwaps(c *gin.Context) {
	store, ok := h.usageFeedStore(c)
	if !ok {
		return
	}
	now := routingNow()
	since, ok := routingSince(c, now, swapLookback)
	if !ok {
		return
	}
	routing, _ := h.routingSnapshot(now)
	lines, truncated, err := readRoutingLines(store, since.Add(-swapStateLead))
	if err != nil {
		log.WithError(err).Error("routing swaps: read feed")
		c.JSON(http.StatusInternalServerError, gin.H{"code": "USAGE_FEED_READ_FAILED"})
		return
	}
	history := deriveRoutingHistory(lines, since, routing)
	swaps := make([]swapView, 0, len(history.swaps))
	for i := len(history.swaps) - 1; i >= 0; i-- {
		swap := history.swaps[i]
		if swap.at.Before(since) {
			continue
		}
		view := swapView{
			At: rfc3339(swap.at), Lane: swap.lane.Lane, Project: swap.lane.Project, Task: swap.lane.Task,
			From:   h.accountView(swap.from.AccountID, swap.from.AccountHash, swap.from.Provider),
			To:     h.accountView(swap.to.AccountID, swap.to.AccountHash, swap.to.Provider),
			Reason: stringPtr(swap.reason), Scope: stringPtr(swap.scope),
		}
		if swap.resetsAt != nil {
			view.ResetsAt = stringPtr(rfc3339(*swap.resetsAt))
		}
		swaps = append(swaps, view)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"read_at": rfc3339(now), "since": rfc3339(since), "truncated": truncated, "swaps": swaps})
}
