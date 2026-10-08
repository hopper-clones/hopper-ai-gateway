package config

import (
	"fmt"
	"net"
	"strings"
)

// ValidateClientHosts accepts literal, specific IP addresses only. A wildcard address would open
// the client API on every interface, and a name could resolve to one, so both are refused.
func ValidateClientHosts(hosts []string) error {
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if host == "" || strings.TrimSpace(host) != host {
			return fmt.Errorf("invalid client-hosts entry %q: expected an IP address", host)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("invalid client-hosts entry %q: expected an IP address, not a name", host)
		}
		if ip.IsUnspecified() {
			return fmt.Errorf("invalid client-hosts entry %q: a wildcard address is not allowed", host)
		}
		if seen[ip.String()] {
			return fmt.Errorf("invalid client-hosts entry %q: listed twice", host)
		}
		seen[ip.String()] = true
	}
	return nil
}
