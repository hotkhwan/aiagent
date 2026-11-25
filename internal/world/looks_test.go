package world

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStaleRelaysAreOnlyRelays(t *testing.T) {
	now := time.Unix(100000, 0)
	row := func(n, dead, att string, ago int) string {
		return n + "|" + dead + "|" + att + "|" + strconv.Itoa(100000-ago) + "\n"
	}
	got := staleRelays(row("aiagent-aaaaaaaa", "1", "0", 90)+row("aiagent-bbbbbbbb", "0", "0", 601)+
		row("aiagent-cccccccc", "0", "0", 300)+row("aiagent-dddddddd", "1", "1", 9999)+row("aiagent-eeeeeeee", "1", "0", 10)+
		row("core", "1", "0", 99999)+row("aiagent-login", "1", "0", 99999), now)
	if want := []string{"aiagent-aaaaaaaa", "aiagent-bbbbbbbb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestShellAndCloudPrefixesAreClosedToUsers(t *testing.T) {
	for _, m := range []string{"!cat x", "  !ls", "\u200b!ls", "& do it"} {
		if userText(m) {
			t.Errorf("%q passed", m)
		}
	}
	if !userText("ทำได้ไหม! a & b") {
		t.Error("plain text was refused")
	}
}

func TestLooksAreSaved(t *testing.T) {
	s := &Service{Store: &Store{dir: t.TempDir()}}
	if err := s.setLook("s1", "cx-fox"); err != nil || s.loadLooks()["s1"] != "cx-fox" {
		t.Fatal(err, s.loadLooks())
	}
	if err := s.setLook("s1", ""); err != nil || len(s.loadLooks()) != 0 {
		t.Fatal(err, s.loadLooks())
	}
}

func TestAdminActsOnRoomsFromTheListWithTheSameLook(t *testing.T) {
	e, _, o := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.store.AddUser("vibe-a", "vibe-a"); err != nil {
		t.Fatal(err)
	}
	var seen []string
	instance(t, o, "vibe-a", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"sessions":[{"sessionId":"11111111-room","displayName":"vibe-a","live":true,"look":"cx-fox","messageVia":"agcmd"}]}}`))
	})
	code, _, body := e.do(t, "GET", apiPrefix+"/snapshot", admin, "")
	if code != 200 || !strings.Contains(body, `"look":"cx-fox"`) || strings.Contains(body, `"readOnly":true`) || !strings.Contains(body, `"messageVia":"agcmd"`) {
		t.Fatalf("admin snapshot = %d %s", code, body)
	}
	seen = nil
	if code, _, _ := e.do(t, "GET", apiPrefix+"/sessions/11111111-room/messages", admin, ""); code != 200 || len(seen) == 0 {
		t.Fatalf("reading a room = %d %v", code, seen)
	}
	seen = nil
	if code, _, _ := e.do(t, "POST", apiPrefix+"/sessions/11111111-room/stop", admin, `{}`); code != 200 || len(seen) != 1 || seen[0] != "POST "+apiPrefix+"/sessions/11111111-room/stop" {
		t.Fatalf("stopping a room from the list = %d %v", code, seen)
	}
}
