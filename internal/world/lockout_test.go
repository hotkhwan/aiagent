package world

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// Only the tunnel, which connects from loopback, may name the visitor. A
// client reaching the service directly is counted by its own address, or it
// could pick a fresh one for every guess and never be locked out.
func TestLockoutKeyTrustsTheVisitorAddressOnlyFromLoopback(t *testing.T) {
	cases := []struct{ peer, cf, want string }{
		{"127.0.0.1", "203.0.113.9", "cf:203.0.113.9"},
		{"::1", "2001:db8::7", "cf:2001:db8::7"},
		{"127.0.0.1", "", "127.0.0.1"},
		{"127.0.0.1", "not-an-ip", "127.0.0.1"},
		{"192.0.2.50", "203.0.113.9", "192.0.2.50"},
		{"192.0.2.50", "", "192.0.2.50"},
	}
	for _, c := range cases {
		if got := lockoutKey(c.peer, c.cf); got != c.want {
			t.Errorf("lockoutKey(%q, %q) = %q, want %q", c.peer, c.cf, got, c.want)
		}
	}
}

// Behind the Cloudflare tunnel every request reaches the service from
// loopback. Counting wrong tokens per peer address put every visitor in one
// bucket: anyone on the internet could guess ten times and lock the owner out.
// Through the tunnel the count is per visitor.
//
// This runs over a real loopback socket, because fiber's in-memory test
// transport reports every peer as 0.0.0.0.
func TestWrongTokensThroughTheTunnelLockOutOnlyThatVisitor(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = e.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = e.app.Shutdown() })
	url := "http://" + ln.Addr().String() + "/aiagent/api/snapshot"
	client := &http.Client{Timeout: 5 * time.Second}

	get := func(visitor, token string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Cf-Connecting-Ip", visitor)
		req.Header.Set(TokenHeader, token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	for range maxFailures {
		if code := get("203.0.113.9", "kaw_wrong"); code != 401 {
			t.Fatalf("a wrong token should be refused with 401, got %d", code)
		}
	}
	if code := get("203.0.113.9", "kaw_wrong"); code != 429 {
		t.Fatalf("the guessing visitor should be locked out, got %d", code)
	}
	if code := get("198.51.100.7", tok); code != 200 {
		t.Fatalf("the owner, from another address, must still get in; got %d", code)
	}
}
