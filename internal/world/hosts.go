// internal/world/hosts.go
//
// An optional Host allowlist (serve --allowed-hosts, AIAGENT_ALLOWED_HOSTS).
// Off by default, so an existing install keeps answering whatever name it is
// reached by. Set, a request whose Host is not on the list and is not a
// loopback name (localhost, 127.0.0.0/8, ::1) is answered 404, like anything
// absent: a DNS-rebinding page or a stray name pointed at the box gets
// nothing. The token is still what protects the API; this narrows the names.
package world

import (
	"fmt"
	"net"
	"strings"
)

// ParseAllowedHosts reads "aiagent.example.com, other.example:443": host
// names, a port ignored, compared case-insensitively.
func ParseAllowedHosts(raw string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		h := hostName(part)
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, " /\\@?#") {
			return nil, fmt.Errorf("allowed host %q is not a host name", strings.TrimSpace(part))
		}
		out = append(out, h)
	}
	return out, nil
}

// hostName is a Host header's name, lower case, without a port or brackets.
func hostName(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
}

// hostAllowed reports whether a request for host may be answered.
func (s *Service) hostAllowed(host string) bool {
	if len(s.AllowedHosts) == 0 {
		return true
	}
	h := hostName(host)
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, a := range s.AllowedHosts {
		if h == a {
			return true
		}
	}
	return false
}
