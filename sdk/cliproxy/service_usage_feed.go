package cliproxy

import (
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage/feed"
	log "github.com/sirupsen/logrus"
)

const usageFeedPluginName = "usage-feed"

// startUsageFeed opens the feed store when usage-feed.enabled is set, registers
// the usage plugin that fills it and hands the store to the management server.
// Changing usage-feed in the config requires a restart.
func (s *Service) startUsageFeed() {
	if s == nil || s.cfg == nil || !s.cfg.UsageFeed.Enabled {
		return
	}
	dir := s.cfg.UsageFeed.ResolveDir(s.configPath)
	store, err := feed.Open(dir)
	if err != nil {
		log.WithError(err).Error("usage feed disabled: store could not be opened")
		return
	}
	s.usageFeed = store
	usage.RegisterNamedPlugin(usageFeedPluginName, feed.NewPlugin(store, s.accountEmailForAuth))
	s.serverOptions = append(s.serverOptions, api.WithUsageFeed(store))
	log.Infof("usage feed enabled (dir=%s)", dir)
}

// stopUsageFeed flushes and closes the feed store.
func (s *Service) stopUsageFeed() {
	if s == nil || s.usageFeed == nil {
		return
	}
	if err := s.usageFeed.Close(); err != nil {
		log.WithError(err).Warn("usage feed: close")
	}
	s.usageFeed = nil
}

// accountEmailForAuth resolves the OAuth account email behind an auth id for
// account hashing. API-key credentials have no account email.
func (s *Service) accountEmailForAuth(authID string) (string, bool) {
	if s == nil || s.coreManager == nil {
		return "", false
	}
	auth, ok := s.coreManager.GetByID(authID)
	if !ok || auth == nil {
		return "", false
	}
	if kind, value := auth.AccountInfo(); kind == "oauth" && value != "" {
		return value, true
	}
	return "", false
}
