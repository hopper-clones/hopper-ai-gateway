package config

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// LanePin prefers one gateway credential for every request that a lane key of
// Lane authenticates. It is a preference, not a lock: when the credential is
// cooling down, exhausted or gone, selection falls back to the configured
// strategy and the lane reports itself as pinned but out.
type LanePin struct {
	// Lane is the lane name carried by the lane key.
	Lane string `yaml:"lane" json:"lane"`
	// AuthID is the gateway credential id the lane prefers.
	AuthID string `yaml:"auth-id" json:"auth-id"`
	// PinnedAt is the RFC 3339 time the pin was set.
	PinnedAt string `yaml:"pinned-at,omitempty" json:"pinned-at,omitempty"`
}

// LanePinFor returns the pin of lane, if any.
func (r RoutingConfig) LanePinFor(lane string) (LanePin, bool) {
	lane = strings.TrimSpace(lane)
	if lane == "" {
		return LanePin{}, false
	}
	for _, pin := range r.LanePins {
		if strings.TrimSpace(pin.Lane) == lane && strings.TrimSpace(pin.AuthID) != "" {
			return pin, true
		}
	}
	return LanePin{}, false
}

// WithLanePin returns a copy of pins with lane pinned to pin, replacing any earlier pin of that lane.
func WithLanePin(pins []LanePin, pin LanePin) []LanePin {
	out := make([]LanePin, 0, len(pins)+1)
	for _, existing := range pins {
		if strings.TrimSpace(existing.Lane) != strings.TrimSpace(pin.Lane) {
			out = append(out, existing)
		}
	}
	return append(out, pin)
}

// WithoutLanePin returns a copy of pins without lane's pin and whether one was removed.
func WithoutLanePin(pins []LanePin, lane string) ([]LanePin, bool) {
	out := make([]LanePin, 0, len(pins))
	removed := false
	for _, existing := range pins {
		if strings.TrimSpace(existing.Lane) == strings.TrimSpace(lane) {
			removed = true
			continue
		}
		out = append(out, existing)
	}
	if len(out) == 0 {
		out = nil
	}
	return out, removed
}

// removeLanePinsWhenNone drops routing.lane-pins from the file when the config
// has no pin left; an omitted list would otherwise keep the old pins on merge.
func removeLanePinsWhenNone(dstRoot, generatedRoot *yaml.Node) {
	if yamlPath(generatedRoot, "routing.lane-pins") != nil {
		return
	}
	if routing := yamlPath(dstRoot, "routing"); routing != nil && routing.Kind == yaml.MappingNode {
		removeMapKey(routing, "lane-pins")
	}
}
