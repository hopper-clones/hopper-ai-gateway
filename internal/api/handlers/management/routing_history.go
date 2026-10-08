package management

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
)

// Swap reasons. A reason is given only when the feed or the pins show it.
const (
	swapReasonExhausted   = "exhausted-window"
	swapReasonUnavailable = "unavailable"
	swapReasonResetFirst  = "reset-first"
	swapReasonPinned      = "pinned"
)

// routingScanCap bounds one routing read so a busy feed cannot stall the handler.
const routingScanCap = 500000

// feedLine is the part of a usage or quota event routing reads.
type feedLine struct {
	Kind        string     `json:"kind"`
	At          feed.Time  `json:"at"`
	Lane        string     `json:"lane"`
	Project     string     `json:"project"`
	Task        string     `json:"task"`
	AccountID   string     `json:"account_id"`
	AccountHash *string    `json:"account_hash"`
	Provider    string     `json:"provider"`
	Model       string     `json:"model"`
	Status      string     `json:"status"`
	Scope       string     `json:"scope"`
	Exhausted   bool       `json:"exhausted"`
	ResetsAt    *feed.Time `json:"resets_at"`
	Tokens      struct {
		Total int64 `json:"total"`
	} `json:"tokens"`
}

func (l feedLine) at() time.Time { return time.Time(l.At) }

// laneRun is one stretch of a lane's requests served by one account.
type laneRun struct {
	account  feedLine
	from     time.Time
	to       time.Time
	requests int
}

// laneHistory is what the feed says about one lane.
type laneHistory struct {
	lane     string
	project  string
	task     string
	runs     []laneRun
	last     feedLine
	requests int
	tokens   int64
}

// routingSwap is one move of a lane from one account to another.
type routingSwap struct {
	at       time.Time
	lane     feedLine
	from     feedLine
	to       feedLine
	reason   string
	scope    string
	resetsAt *time.Time
}

// routingHistory is derived from one ordered pass over the feed.
type routingHistory struct {
	lanes     map[string]*laneHistory
	swaps     []routingSwap
	truncated bool
}

// readRoutingLines scans the feed from `from` and returns usage and quota lines at or after it, oldest first.
func readRoutingLines(store *feed.Store, from time.Time) ([]feedLine, bool, error) {
	lines := make([]feedLine, 0, 1024)
	truncated, err := store.Scan(from, routingScanCap, func(raw json.RawMessage) {
		var line feedLine
		if json.Unmarshal(raw, &line) != nil || line.at().Before(from) {
			return
		}
		if line.Kind == feed.KindUsage || line.Kind == feed.KindQuota {
			lines = append(lines, line)
		}
	})
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at().Before(lines[j].at()) })
	return lines, truncated, nil
}

// deriveRoutingHistory walks ordered feed lines. countFrom limits the request
// and token counts (for example to today); runs and swaps use every line.
func deriveRoutingHistory(lines []feedLine, countFrom time.Time, routing config.RoutingConfig) routingHistory {
	history := routingHistory{lanes: map[string]*laneHistory{}}
	quotas := map[string]map[string]feedLine{}
	lastStatus := map[string]string{}
	for _, line := range lines {
		if line.Kind == feed.KindQuota {
			if quotas[line.AccountID] == nil {
				quotas[line.AccountID] = map[string]feedLine{}
			}
			quotas[line.AccountID][line.Scope] = line
			continue
		}
		if lane := strings.TrimSpace(line.Lane); lane != "" && line.AccountID != "" {
			entry := history.lanes[lane]
			if entry == nil {
				entry = &laneHistory{lane: lane}
				history.lanes[lane] = entry
			}
			if n := len(entry.runs); n > 0 && entry.runs[n-1].account.AccountID != line.AccountID {
				history.swaps = append(history.swaps, swapOf(entry, line, quotas, lastStatus, routing))
			}
			if n := len(entry.runs); n == 0 || entry.runs[n-1].account.AccountID != line.AccountID {
				entry.runs = append(entry.runs, laneRun{account: line, from: line.at()})
			}
			run := &entry.runs[len(entry.runs)-1]
			run.to = line.at()
			run.requests++
			entry.last = line
			if line.Project != "" {
				entry.project = line.Project
			}
			if line.Task != "" {
				entry.task = line.Task
			}
			if !line.at().Before(countFrom) {
				entry.requests++
				entry.tokens += line.Tokens.Total
			}
		}
		if line.AccountID != "" {
			lastStatus[line.AccountID] = line.Status
		}
	}
	return history
}

// swapOf explains why line moved its lane off the lane's previous account.
func swapOf(entry *laneHistory, line feedLine, quotas map[string]map[string]feedLine, lastStatus map[string]string, routing config.RoutingConfig) routingSwap {
	previous := entry.runs[len(entry.runs)-1].account
	swap := routingSwap{at: line.at(), lane: line, from: previous, to: line}
	if pin, ok := routing.LanePinFor(entry.lane); ok && pin.AuthID == line.AccountID {
		if pinnedAt, err := time.Parse(time.RFC3339, pin.PinnedAt); err == nil && pinnedAt.After(entry.last.at()) && !pinnedAt.After(line.at()) {
			swap.reason = swapReasonPinned
			return swap
		}
	}
	scopes := make([]string, 0, len(quotas[previous.AccountID]))
	for scope := range quotas[previous.AccountID] {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		quota := quotas[previous.AccountID][scope]
		if !quota.Exhausted || (quota.ResetsAt != nil && !time.Time(*quota.ResetsAt).After(line.at())) {
			continue
		}
		swap.reason, swap.scope = swapReasonExhausted, scope
		if quota.ResetsAt != nil {
			resetsAt := time.Time(*quota.ResetsAt)
			swap.resetsAt = &resetsAt
		}
		return swap
	}
	if lastStatus[previous.AccountID] == "error" {
		swap.reason = swapReasonUnavailable
		return swap
	}
	if strings.EqualFold(strings.TrimSpace(routing.Strategy), "reset-first") {
		swap.reason = swapReasonResetFirst
	}
	return swap
}
