// internal/world/previewport.go
//
// Which server a room may show: one listening on a loopback port in a fixed
// range, whose listening socket belongs to that room's uid. Ports are not
// anyone's property, so another room could listen on the port this one named
// and have its page shown, or its server reached, in this one's place. The
// owner is read from the kernel right before and right after connecting,
// before a byte is sent.
package world

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const previewPortMin, previewPortMax = 3000, 5999

func previewPortOK(port int) bool { return port >= previewPortMin && port <= previewPortMax }

type listenSocket struct {
	addr netip.Addr
	uid  uint32
}

// listeners reads every socket listening on port from /proc/net/tcp and tcp6.
func listeners(procRoot string, port int) ([]listenSocket, error) {
	var out []listenSocket
	for _, name := range []string{"tcp", "tcp6"} {
		f, err := os.Open(filepath.Join(procRoot, "net", name))
		if os.IsNotExist(err) && name == "tcp6" {
			continue // no IPv6 on this machine
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for first := true; sc.Scan(); first = false {
			if first {
				continue // column names
			}
			fl := strings.Fields(sc.Text())
			if len(fl) < 8 || fl[3] != "0A" {
				continue
			}
			addr, p, err := procAddr(fl[1])
			if err == nil && p != port {
				continue
			}
			uid, uerr := strconv.ParseUint(fl[7], 10, 32)
			if err != nil || uerr != nil {
				f.Close()
				return nil, fmt.Errorf("unreadable line in %s", name)
			}
			out = append(out, listenSocket{addr, uint32(uid)})
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// procAddr reads "0100007F:0BB8": 32-bit words in host order, so each four
// bytes are reversed (little-endian machines, all this runs on).
func procAddr(s string) (netip.Addr, int, error) {
	h, p, ok := strings.Cut(s, ":")
	b, err := hex.DecodeString(h)
	port, perr := strconv.ParseUint(p, 16, 16)
	if !ok || err != nil || perr != nil || (len(b) != 4 && len(b) != 16) {
		return netip.Addr{}, 0, fmt.Errorf("bad address")
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	a, _ := netip.AddrFromSlice(b)
	return a.Unmap(), int(port), nil
}

// roomListener is the loopback address to reach the room's server at. Every
// socket the kernel could hand that connection to must be the room's: the
// exact address, and a wildcard of either family.
func roomListener(procRoot string, port int, uid uint32) (netip.AddrPort, error) {
	if !previewPortOK(port) {
		return netip.AddrPort{}, fmt.Errorf("port outside %d-%d", previewPortMin, previewPortMax)
	}
	ls, err := listeners(procRoot, port)
	if err != nil {
		return netip.AddrPort{}, err
	}
	for _, want := range []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.IPv6Loopback()} {
		found := false
		for _, l := range ls {
			// [::] also takes IPv4 unless told not to, which /proc does not show
			if l.addr != want && !l.addr.IsUnspecified() {
				continue
			}
			if l.addr.IsUnspecified() && l.addr.Is4() && want.Is6() {
				continue
			}
			if l.uid != uid {
				return netip.AddrPort{}, fmt.Errorf("port %d is another account's", port)
			}
			found = true
		}
		if found {
			return netip.AddrPortFrom(want, uint16(port)), nil
		}
	}
	return netip.AddrPort{}, fmt.Errorf("nothing listens on port %d", port)
}

// dialRoom connects to the room's own server on port, or not at all.
func (p *Preview) dialRoom(ctx context.Context, room string, port int) (net.Conn, error) {
	uid, err := p.uidOf(room)
	if err != nil {
		return nil, err
	}
	if uid < roomUIDMin || uid > roomUIDMax {
		return nil, fmt.Errorf("not a room's uid")
	}
	addr, err := roomListener(p.procRoot, port, uid)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		return nil, err
	}
	again, err := roomListener(p.procRoot, port, uid)
	if err != nil || again != addr {
		conn.Close()
		return nil, fmt.Errorf("the server on port %d changed hands", port)
	}
	return conn, nil
}
