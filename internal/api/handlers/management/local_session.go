package management

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

const localSessionCookie = "hopper_local_management"
const localSessionHeader = "X-Hopper-Local-Session"
const localSessionLifetime = 12 * time.Hour

// Local browser credentials exist only in memory, independently of the management key.
type localBrowserSession struct {
	mu                sync.Mutex
	origin            string
	capability        [32]byte
	capabilityExpires time.Time
	session           [32]byte
	sessionExpires    time.Time
	now               func() time.Time
	policy            [32]byte
}

func randomLocalCredential() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate local browser credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

// CreateLocalConsoleURL mints a one-use launch capability. Call only from the native launcher.
// The returned URL is sensitive and must never be printed or persisted.
func (h *Handler) CreateLocalConsoleURL(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" {
		return "", fmt.Errorf("local console requires an explicit loopback origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("local console requires a literal loopback address")
	}
	capability, err := randomLocalCredential()
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.localBrowser.mu.Lock()
	defer h.localBrowser.mu.Unlock()
	h.localBrowser.policy = localSessionPolicy(h.cfg)
	h.localBrowser.origin = origin
	h.localBrowser.capability = sha256.Sum256([]byte(capability))
	h.localBrowser.capabilityExpires = h.localBrowser.timeNow().Add(5 * time.Minute)
	return origin + "/management.html#/quota?local-session=" + capability, nil
}

func (s *localBrowserSession) timeNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (h *Handler) revokeLocalBrowser() {
	h.localBrowser.mu.Lock()
	defer h.localBrowser.mu.Unlock()
	h.localBrowser.capabilityExpires = time.Time{}
	h.localBrowser.sessionExpires = time.Time{}
}

// localRequestLocked uses the socket peer, never proxy-controlled ClientIP headers.
func (s *localBrowserSession) localRequestLocked(r *http.Request, requireOrigin bool) bool {
	if s.origin == "" {
		return false
	}
	origin, err := url.Parse(s.origin)
	if err != nil || r.Host != origin.Host {
		return false
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(peer)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	if r.Header.Get(localSessionHeader) != "1" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" || requireOrigin {
		if o != s.origin {
			return false
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	return true
}

func (h *Handler) acceptsLocalSession(r *http.Request) bool {
	s := &h.localBrowser
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/v8/management/") || !s.localRequestLocked(r, r.Method != http.MethodGet && r.Method != http.MethodHead) || !s.timeNow().Before(s.sessionExpires) {
		return false
	}
	cookie, err := r.Cookie(localSessionCookie)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	return subtle.ConstantTimeCompare(digest[:], s.session[:]) == 1
}

// LocalSession handles launch exchange, authenticated restoration and revocation.
func (h *Handler) LocalSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	if c.Request.Method == http.MethodGet {
		if !h.acceptsLocalSession(c.Request) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.JSON(http.StatusOK, gin.H{"authenticated": true})
		return
	}
	s := &h.localBrowser
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.localRequestLocked(c.Request, true) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	secure := strings.HasPrefix(s.origin, "https:")
	if c.Request.Method == http.MethodDelete {
		cookie, err := c.Request.Cookie(localSessionCookie)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		digest := sha256.Sum256([]byte(cookie.Value))
		if !s.timeNow().Before(s.sessionExpires) || subtle.ConstantTimeCompare(digest[:], s.session[:]) != 1 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		s.sessionExpires = time.Time{}
		http.SetCookie(c.Writer, &http.Cookie{Name: localSessionCookie, Value: "", Path: "/v8/management", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		c.Status(http.StatusNoContent)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var body struct {
		Capability string `json:"capability"`
	}
	if c.ContentType() != "application/json" || c.ShouldBindJSON(&body) != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	digest := sha256.Sum256([]byte(body.Capability))
	if !s.timeNow().Before(s.capabilityExpires) || subtle.ConstantTimeCompare(digest[:], s.capability[:]) != 1 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	credential, err := randomLocalCredential()
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.capabilityExpires = time.Time{}
	s.session = sha256.Sum256([]byte(credential))
	s.sessionExpires = s.timeNow().Add(localSessionLifetime)
	http.SetCookie(c.Writer, &http.Cookie{Name: localSessionCookie, Value: credential, Path: "/v8/management", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: int(localSessionLifetime.Seconds())})
	c.JSON(http.StatusOK, gin.H{"authenticated": true})
}

// RevokeLocalConsoleSession invalidates launch capabilities and browser sessions.
func (h *Handler) RevokeLocalConsoleSession() { h.revokeLocalBrowser() }

func localSessionPolicy(cfg *config.Config) [32]byte {
	if cfg == nil {
		return [32]byte{}
	}
	return sha256.Sum256([]byte(fmt.Sprintf("%v|%v|%v|%v|%v", cfg.RemoteManagement.SecretKey, cfg.RemoteManagement.AllowRemote, cfg.RemoteManagement.DisableControlPanel, cfg.Home.Enabled, cfg.Host)))
}
