// internal/world/proxies.go
//
// Who is knocking, behind a reverse proxy. Wrong tokens are counted per
// visitor (http.go gate); behind a proxy every request comes from the proxy,
// so its address alone would put every visitor in one bucket and let anyone
// lock the owner out. Two proxies may name the visitor:
//
//   - the Cloudflare tunnel, which connects from loopback (Cf-Connecting-Ip);
//   - a proxy the owner named with --trusted-proxies (an Istio gateway, Caddy):
//     the right-most X-Forwarded-For address that is not itself trusted. The
//     left-most entries are whatever the client wrote and are never believed.
package world

import (
	"fmt"
	"net"
	"strings"
	"sync/atomic"
)

// trustedProxies is set once at start (serve's --trusted-proxies).
var trustedProxies atomic.Pointer[[]*net.IPNet]

// SetTrustedProxies names the proxies whose X-Forwarded-For is believed.
func SetTrustedProxies(nets []*net.IPNet) { trustedProxies.Store(&nets) }

func currentTrustedProxies() []*net.IPNet {
	if p := trustedProxies.Load(); p != nil {
		return *p
	}
	return nil
}

// ParseTrustedProxies reads "10.42.0.0/16, 192.0.2.10": CIDRs or single
// addresses. Everything (/0) is refused: that would believe any client.
func ParseTrustedProxies(raw string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, fmt.Errorf("trusted proxy %q is not an address or CIDR", part)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is not an address or CIDR", part)
		}
		if ones, _ := n.Mask.Size(); ones == 0 {
			return nil, fmt.Errorf("trusted proxy %q would trust every address", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// visitorKey is the address wrong tokens are counted under.
func visitorKey(peer, cfConnectingIP, forwardedFor string, trusted []*net.IPNet) string {
	ip := net.ParseIP(peer)
	if ip != nil && ip.IsLoopback() {
		if v := net.ParseIP(strings.TrimSpace(cfConnectingIP)); v != nil {
			return "cf:" + v.String()
		}
	}
	if ip == nil || !inNets(ip, trusted) || forwardedFor == "" {
		return peer
	}
	hops := strings.Split(forwardedFor, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := net.ParseIP(strings.TrimSpace(hops[i]))
		if hop == nil {
			return peer // a hop we cannot read: believe nothing further left
		}
		if !inNets(hop, trusted) {
			return "xff:" + hop.String()
		}
	}
	return peer
}
