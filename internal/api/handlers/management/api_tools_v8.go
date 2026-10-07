package management

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// APICallV8 retains generic API-call behavior and observes successful native quota
// refreshes. Display-only plugin bucket labels are never interpreted as limits.
func (h *Handler) APICallV8(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	var request apiCallRequest
	if json.Unmarshal(raw, &request) != nil {
		h.APICall(c)
		return
	}
	auth := h.authByIndex(firstNonEmptyString(request.AuthIndexSnake, request.AuthIndexCamel, request.AuthIndexPascal))
	if auth == nil || !nativeQuotaRequest(auth.Provider, request.Method, request.URL) {
		h.APICall(c)
		return
	}
	observedAt := time.Now()
	writer := &quotaResponseCapture{ResponseWriter: c.Writer}
	c.Writer = writer
	defer func() {
		c.Writer = writer.ResponseWriter
		// Commit the quota refresh only after its routing observation is visible.
		_, _ = writer.ResponseWriter.Write(writer.body.Bytes())
	}()
	h.APICall(c)
	if writer.Status() != http.StatusOK {
		return
	}
	var response apiCallResponse
	if json.Unmarshal(writer.body.Bytes(), &response) != nil {
		return
	}
	observation := h.observeNativeQuotaResponse(c, auth, request, response, observedAt)
	annotated, err := json.Marshal(struct {
		apiCallResponse
		RoutingObservation quotaRefreshObservation `json:"routing_observation"`
	}{response, observation})
	if err == nil {
		writer.body.Reset()
		writer.body.Write(annotated)
	}
}

type quotaResponseCapture struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *quotaResponseCapture) Write(body []byte) (int, error) {
	w.body.Write(body)
	return len(body), nil
}
func (w *quotaResponseCapture) WriteString(body string) (int, error) {
	w.body.WriteString(body)
	return len(body), nil
}

func (w *quotaResponseCapture) WriteHeaderNow() {}

func nativeQuotaRequest(provider, method, rawURL string) bool {
	if strings.ToUpper(strings.TrimSpace(method)) != http.MethodGet {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(provider) {
	case "codex":
		return strings.EqualFold(u.Hostname(), "chatgpt.com") && u.Path == "/backend-api/wham/usage"
	case "claude":
		return strings.EqualFold(u.Hostname(), "api.anthropic.com") && u.Path == "/api/oauth/usage"
	}
	return false
}

type quotaRefreshObservation struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (h *Handler) observeNativeQuotaResponse(c *gin.Context, auth *coreauth.Auth, request apiCallRequest, response apiCallResponse, at time.Time) quotaRefreshObservation {
	if h.authManager == nil || auth == nil || !nativeQuotaRequest(auth.Provider, request.Method, request.URL) || response.StatusCode < 200 || response.StatusCode >= 300 {
		return quotaRefreshObservation{"unavailable", "no_successful_native_quota_response"}
	}
	headers, ok := nativeQuotaSignals(auth, []byte(response.Body), at)
	if !ok {
		return quotaRefreshObservation{"unsupported", "incomplete_invalid_or_oversized_quota_snapshot"}
	}
	if err := h.authManager.ObserveQuotaHeaders(c.Request.Context(), auth.ID, auth.Provider, headers, at); err != nil {
		log.WithError(err).Debug("quota refresh observation rejected")
		return quotaRefreshObservation{"rejected", "routing_observation_not_applied"}
	}
	if latest, ok := h.authManager.GetByID(auth.ID); ok && latest.Quota.ObservedAt.After(at) {
		return quotaRefreshObservation{"superseded", "newer_observation_retained"}
	}
	return quotaRefreshObservation{"applied", ""}
}

// Native JSON endpoints report percentages; Claude response headers instead
// report fractions. Only explicit provider field names carry model scope.
func nativeQuotaSignals(auth *coreauth.Auth, body []byte, at time.Time) (http.Header, bool) {
	if !gjson.ValidBytes(body) {
		return nil, false
	}
	root := gjson.ParseBytes(body)
	if strings.EqualFold(auth.Provider, "claude") {
		// Fable's cap is provider-named inside a weekly_scoped model limit.
		// An opaque bucket ID or a UI display group is never treated as this model.
		limits := root.Get("limits")
		var candidates []gjson.Result
		for _, limit := range limits.Array() {
			name := strings.ToLower(strings.TrimSpace(limit.Get("scope.model.display_name").String()))
			if limit.Get("kind").String() == "weekly_scoped" && (name == "fable" || name == "fable 5") {
				candidates = append(candidates, limit)
			}
		}
		var selected gjson.Result
		for _, candidate := range candidates {
			if candidate.Get("is_active").Type == gjson.True {
				if selected.Exists() {
					return nil, false
				}
				selected = candidate
			}
		}
		if !selected.Exists() && len(candidates) == 1 {
			selected = candidates[0]
		}
		if len(candidates) > 1 && !selected.Exists() {
			return nil, false
		}
		if selected.Exists() {
			nativeWindow := map[string]json.RawMessage{"utilization": json.RawMessage(selected.Get("percent").Raw), "resets_at": json.RawMessage(selected.Get("resets_at").Raw)}
			encoded, err := json.Marshal(nativeWindow)
			if err != nil {
				return nil, false
			}
			var object map[string]json.RawMessage
			if json.Unmarshal(body, &object) != nil {
				return nil, false
			}
			object["seven_day_fable"] = encoded
			updated, err := json.Marshal(object)
			if err != nil {
				return nil, false
			}
			root = gjson.ParseBytes(updated)
		}
	}
	if strings.EqualFold(auth.Provider, "claude") && !root.Get("seven_day_fable").Exists() {
		for key := range auth.Quota.Signals {
			if strings.HasPrefix(strings.ToLower(key), "anthropic-ratelimit-unified-7d_oi-") {
				return nil, false
			}
		}
	}
	headers := make(http.Header)
	number := func(node gjson.Result, max float64) (float64, bool) {
		value := node.Float()
		return value, node.Type == gjson.Number && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= max
	}
	reset := func(node gjson.Result) (string, bool) {
		if node.Type == gjson.Null && node.Exists() {
			return "", true
		}
		if node.Type == gjson.Number {
			n := node.Float()
			if n > 0 && n <= 253402300799 && n == math.Trunc(n) {
				return strconv.FormatInt(node.Int(), 10), true
			}
		}
		if node.Type == gjson.String {
			if value, err := time.Parse(time.RFC3339, node.String()); err == nil {
				return strconv.FormatInt(value.Unix(), 10), true
			}
		}
		return "", false
	}
	if strings.EqualFold(auth.Provider, "claude") {
		for _, scope := range []struct {
			key, header string
			required    bool
		}{
			{"five_hour", "5h", true}, {"seven_day", "7d", true}, {"seven_day_sonnet", "7d-sonnet", false}, {"seven_day_opus", "7d-opus", false}, {"seven_day_fable", "7d-fable", false},
		} {
			node := root.Get(scope.key)
			prefix := "Anthropic-Ratelimit-Unified-" + scope.header
			if !node.Exists() {
				if scope.required {
					return nil, false
				}
				for key := range auth.Quota.Signals {
					if strings.HasPrefix(strings.ToLower(key), strings.ToLower(prefix)+"-") {
						return nil, false
					}
				}
				continue
			}
			if node.Type == gjson.Null {
				// Explicit null means this provider snapshot has no such window.
				continue
			}
			used, ok := number(node.Get("utilization"), 100)
			if !ok {
				return nil, false
			}
			deadline, ok := reset(node.Get("resets_at"))
			if !ok {
				return nil, false
			}
			headers.Set(prefix+"-Utilization", strconv.FormatFloat(used/100, 'f', -1, 64))
			if deadline != "" {
				headers.Set(prefix+"-Reset", deadline)
			}
		}
	} else if strings.EqualFold(auth.Provider, "codex") {
		addLimit := func(node gjson.Result, prefix string) bool {
			if !node.IsObject() {
				return false
			}
			for _, slot := range []string{"primary", "secondary"} {
				window := node.Get(slot + "_window")
				if !window.Exists() {
					return false
				}
				if window.Type == gjson.Null {
					continue
				}
				used, ok := number(window.Get("used_percent"), 100)
				if !ok {
					return false
				}
				deadline, ok := reset(window.Get("reset_at"))
				if !ok {
					seconds, valid := number(window.Get("reset_after_seconds"), 365*24*60*60)
					if !valid {
						return false
					}
					deadline = strconv.FormatInt(at.Add(time.Duration(seconds*float64(time.Second))).Unix(), 10)
				}
				headers.Set(prefix+"-"+slot+"-Used-Percent", strconv.FormatFloat(used, 'f', -1, 64))
				if deadline != "" {
					headers.Set(prefix+"-"+slot+"-Reset-At", deadline)
				}
			}
			return true
		}
		if !addLimit(root.Get("rate_limit"), "X-Codex") {
			return nil, false
		}
		extra := root.Get("additional_rate_limits")
		if !extra.Exists() {
			for key := range auth.Quota.Signals {
				if strings.HasSuffix(strings.ToLower(key), "-limit-name") {
					return nil, false
				}
			}
		} else if extra.Type != gjson.Null {
			if !extra.IsArray() {
				return nil, false
			}
			for i, limit := range extra.Array() {
				name := limit.Get("limit_name")
				if name.Type != gjson.String || strings.TrimSpace(name.String()) == "" || len(name.String()) > 512 || strings.IndexFunc(name.String(), func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0 {
					return nil, false
				}
				prefix := "X-Codex-Quota" + strconv.Itoa(i)
				headers.Set(prefix+"-Limit-Name", name.String())
				if !addLimit(limit.Get("rate_limit"), prefix) {
					return nil, false
				}
			}
		}
	} else {
		return nil, false
	}
	// The canonical auth observation store admits at most 64 headers. A larger
	// native snapshot must remain unsupported rather than lose named model caps.
	if len(headers) == 0 || len(headers) > 64 {
		return nil, false
	}
	return headers, true
}
