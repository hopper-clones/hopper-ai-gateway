package cliproxy

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/capacitycodex"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/watcher"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// startCapacityCodex registers the Codex logins AI Capacity tracks when
// capacity-codex.enabled is set. Changing it in the config requires a restart.
func (s *Service) startCapacityCodex(ctx context.Context) {
	if s == nil || s.cfg == nil || !s.cfg.CapacityCodex.Enabled {
		return
	}
	reader, err := capacitycodex.CommandReader()
	if err != nil {
		log.Warnf("capacity-codex disabled: %s", err.Error())
		return
	}
	upsert := func(auth *coreauth.Auth) {
		s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: watcher.AuthUpdateActionAdd, ID: auth.ID, Auth: auth})
	}
	remove := func(id string) {
		s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: watcher.AuthUpdateActionDelete, ID: id})
	}
	source := capacitycodex.NewSource(reader, capacitycodex.CodexRefresher(s.cfg), upsert, remove)
	interval := s.cfg.CapacityCodex.RefreshInterval()
	log.Infof("capacity-codex enabled (refresh=%s)", interval)
	go source.Run(ctx, interval)
}
