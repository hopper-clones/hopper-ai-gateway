package api

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// clientListenerRetry is how long a client listener waits before trying again to bind an address
// that is not up yet (a private network interface that comes up after the gateway).
var clientListenerRetry = 15 * time.Second

// clientAPIPrefixes are the only paths a client listener serves. Everything else, including the
// management API, the control panel, OAuth callbacks and the root page, answers 404 there.
var clientAPIPrefixes = []string{"/v1/", "/v1beta/", "/openai/v1/", "/backend-api/codex/"}

// clientAPIPath reports whether a request path belongs to the client API. Paths that are not
// already clean (dot segments, repeated slashes) are refused rather than interpreted.
func clientAPIPath(requestPath string) bool {
	if requestPath == "/healthz" {
		return true
	}
	if requestPath == "" || strings.Contains(requestPath, "\\") {
		return false
	}
	cleaned := path.Clean(requestPath)
	if cleaned != requestPath && cleaned+"/" != requestPath {
		return false
	}
	for _, prefix := range clientAPIPrefixes {
		if strings.HasPrefix(requestPath, prefix) {
			return true
		}
	}
	return false
}

// clientOnlyHandler serves the client API through next and answers 404 to every other path.
func clientOnlyHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL == nil || !clientAPIPath(r.URL.Path) || (r.URL.RawPath != "" && !clientAPIPath(r.URL.RawPath)) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// startClientListeners serves the client API on each configured client host, on the main port.
// A host that cannot be bound yet is retried until the server stops; the main listener is never
// affected by a client listener's failure.
func (s *Server) startClientListeners(port int, tlsConfig *tls.Config) {
	cfg := s.getConfig()
	if cfg == nil || len(cfg.ClientHosts) == 0 || s.server == nil {
		return
	}
	handler := clientOnlyHandler(s.server.Handler)
	for _, host := range cfg.ClientHosts {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 30 * time.Second}
		s.listenerMu.Lock()
		if s.clientStopped {
			s.listenerMu.Unlock()
			return
		}
		s.clientServers = append(s.clientServers, server)
		s.listenerMu.Unlock()
		go s.serveClientListener(server, tlsConfig)
	}
}

func (s *Server) serveClientListener(server *http.Server, tlsConfig *tls.Config) {
	warned := false
	for {
		listener, errListen := net.Listen("tcp", server.Addr)
		if errListen == nil {
			if tlsConfig != nil {
				listener = tls.NewListener(listener, tlsConfig)
			}
			log.Infof("client API listener started on %s", server.Addr)
			errServe := server.Serve(listener)
			if errServe == nil || errors.Is(errServe, http.ErrServerClosed) {
				return
			}
			log.Warnf("client API listener on %s stopped: %v", server.Addr, errServe)
			return
		}
		if !warned {
			log.Warnf("client API listener on %s not started yet: %v (retrying)", server.Addr, errListen)
			warned = true
		}
		s.listenerMu.Lock()
		stopped := s.clientStopped
		s.listenerMu.Unlock()
		if stopped {
			return
		}
		time.Sleep(clientListenerRetry)
		s.listenerMu.Lock()
		stopped = s.clientStopped
		s.listenerMu.Unlock()
		if stopped {
			return
		}
	}
}

// stopClientListeners closes every client listener; later binds are not attempted.
func (s *Server) stopClientListeners() {
	s.listenerMu.Lock()
	servers := s.clientServers
	s.clientServers = nil
	s.clientStopped = true
	s.listenerMu.Unlock()
	for _, server := range servers {
		if errClose := server.Close(); errClose != nil && !errors.Is(errClose, http.ErrServerClosed) && !errors.Is(errClose, net.ErrClosed) {
			log.Debugf("failed to close client API listener %s: %v", server.Addr, errClose)
		}
	}
}
