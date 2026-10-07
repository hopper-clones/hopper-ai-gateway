package management

import (
	"net"

	"github.com/gin-gonic/gin"
)

// loopbackPeer reports whether the actual socket peer is a loopback address.
// Forwarded headers never establish local access.
func loopbackPeer(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	peer, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(peer)
	return ip != nil && ip.IsLoopback()
}
