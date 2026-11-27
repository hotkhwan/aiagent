package world

import (
	"net"
	"testing"
)

func mustCIDRs(t *testing.T, raw string) []*net.IPNet {
	t.Helper()
	nets, err := ParseTrustedProxies(raw)
	if err != nil {
		t.Fatal(err)
	}
	return nets
}

// Behind a reverse proxy that is not the Cloudflare tunnel (an Istio or
// Caddy in front of aiagent), every request comes from the proxy. Counting
// wrong tokens per peer would put every visitor in one bucket, so anyone could
// lock the owner out. A proxy the owner named is believed about the visitor:
// the right-most X-Forwarded-For address that is not itself a trusted proxy.
func TestVisitorKeyBelievesOnlyNamedProxies(t *testing.T) {
	trusted := mustCIDRs(t, "10.42.0.0/16, 192.0.2.10/32")
	cases := []struct{ name, peer, cf, xff, want string }{
		{"a named proxy names the visitor", "10.42.0.42", "", "203.0.113.9", "xff:203.0.113.9"},
		{"the right-most untrusted hop wins", "10.42.0.42", "", "198.51.100.1, 203.0.113.9", "xff:203.0.113.9"},
		{"a forged left-most entry is ignored", "10.42.0.42", "", "1.2.3.4, 203.0.113.9, 10.42.0.7", "xff:203.0.113.9"},
		{"every hop trusted: the peer counts", "10.42.0.42", "", "10.42.0.7", "10.42.0.42"},
		{"garbage header: the peer counts", "10.42.0.42", "", "not-an-ip", "10.42.0.42"},
		{"no header: the peer counts", "10.42.0.42", "", "", "10.42.0.42"},
		{"a stranger cannot name a visitor", "198.51.100.66", "", "203.0.113.9", "198.51.100.66"},
		{"a single trusted host", "192.0.2.10", "", "203.0.113.5", "xff:203.0.113.5"},
		{"the tunnel rule is unchanged", "127.0.0.1", "203.0.113.9", "", "cf:203.0.113.9"},
	}
	for _, c := range cases {
		if got := visitorKey(c.peer, c.cf, c.xff, trusted); got != c.want {
			t.Errorf("%s: visitorKey(%q, %q, %q) = %q, want %q", c.name, c.peer, c.cf, c.xff, got, c.want)
		}
	}
	// nothing named: X-Forwarded-For is never believed
	if got := visitorKey("10.42.0.42", "", "203.0.113.9", nil); got != "10.42.0.42" {
		t.Errorf("without trusted proxies the header must be ignored, got %q", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	if nets := mustCIDRs(t, ""); len(nets) != 0 {
		t.Fatalf("empty: %v", nets)
	}
	if nets := mustCIDRs(t, "10.0.0.5"); len(nets) != 1 || nets[0].String() != "10.0.0.5/32" {
		t.Fatalf("a bare address is one host: %v", nets)
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "nonsense", "10.0.0.0/33"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
