package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hotkhwan/aiagent/internal/world"
)

// The rooms osuser.sh makes: in this group, with a uid in this range.
const (
	roomGroup  = "aiagent-users"
	roomUIDMin = 700000
	roomUIDMax = 799999
	// far above 30 environments of 60 variables of 4096 bytes
	maxRoomEnvInput = 16 << 20
)

// roomEnvSync gives every room the environment files it should hold
// (internal/world/roomenv.go). Root only: aiagent-room-env.service runs it.
//
//	aiagent room-env-sync [--state-dir DIR] [--room-duties FILE] [--listing FILE]
func roomEnvSync(args []string) {
	fs := flag.NewFlagSet("aiagent room-env-sync", flag.ExitOnError)
	stateDir := stateDirFlag(fs)
	duties := fs.String("room-duties", "/etc/aiagent/room-duties.conf", "each room's duty")
	listing := fs.String("listing", "/etc/aiagent/room-environments.conf", "names and tiers for room-share.py (no values)")
	_ = fs.Parse(args)
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "aiagent room-env-sync: run as root (aiagent-room-env.service)")
		os.Exit(2)
	}
	dir := *stateDir
	if dir == "" {
		dir = world.DefaultStateDir()
	}
	svc := &world.Service{Store: world.NewStore(dir), Duties: &world.RoomDuties{Path: *duties}}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiagent room-env-sync: cannot find this binary:", err)
		os.Exit(1)
	}
	failed := false
	if b, err := svc.RoomEnvListing(); err != nil {
		fmt.Fprintln(os.Stderr, "aiagent room-env-sync: listing:", err)
		failed = true
	} else if err := writePublic(*listing, b); err != nil {
		fmt.Fprintln(os.Stderr, "aiagent room-env-sync: listing:", err)
		failed = true
	}
	// one line per failed room, naming the room and never a value
	for _, err := range svc.SyncRoomEnv(func(room string, files map[string][]byte) error { return deliverAsRoom(exe, room, files) }) {
		fmt.Fprintln(os.Stderr, "aiagent room-env-sync:", err)
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}

// roomAccount is a room's uid, gid and home, only for an account osuser.sh
// made: in the rooms' group, in the rooms' uid range, at /home/<room>.
func roomAccount(room string) (uid, gid uint32, home string, err error) {
	u, err := user.Lookup(room)
	if err != nil {
		return 0, 0, "", errors.New("no such user (the room is gone)")
	}
	g, err := user.LookupGroup(roomGroup)
	if err != nil {
		return 0, 0, "", errors.New("no group " + roomGroup)
	}
	gids, err := u.GroupIds()
	if err != nil || !slices.Contains(gids, g.Gid) {
		return 0, 0, "", errors.New("not in " + roomGroup + ", refusing")
	}
	n, err1 := strconv.ParseUint(u.Uid, 10, 32)
	m, err2 := strconv.ParseUint(u.Gid, 10, 32)
	if err1 != nil || err2 != nil || n < roomUIDMin || n > roomUIDMax {
		return 0, 0, "", errors.New("uid outside the rooms' range, refusing")
	}
	if u.HomeDir != "/home/"+room {
		return 0, 0, "", errors.New("home is not /home/" + room + ", refusing")
	}
	return uint32(n), uint32(m), u.HomeDir, nil
}

// deliverAsRoom runs `aiagent room-env-write` as the room's own user, with no
// supplementary groups, the files on stdin and nothing else in its
// environment.
func deliverAsRoom(exe, room string, files map[string][]byte) error {
	uid, gid, home, err := roomAccount(room)
	if err != nil {
		return err
	}
	in := map[string]string{}
	for k, v := range files {
		in[k] = string(v)
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "room-env-write")
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Dir = "/"
	cmd.Stdin = strings.NewReader(string(b))
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("writer: %v: %s", err, strings.TrimSpace(string(out[:min(len(out), 300)])))
	}
	return nil
}

// roomEnvWrite is the writer: as the room's user, it makes
// $HOME/.config/aiagent/env hold exactly the files on stdin. It refuses to
// run as root, which would follow whatever the room left in its home.
func roomEnvWrite(args []string) {
	fs := flag.NewFlagSet("aiagent room-env-write", flag.ExitOnError)
	_ = fs.Parse(args)
	if os.Geteuid() == 0 {
		fmt.Fprintln(os.Stderr, "aiagent room-env-write: never as root")
		os.Exit(2)
	}
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) {
		fmt.Fprintln(os.Stderr, "aiagent room-env-write: HOME is not set")
		os.Exit(2)
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, maxRoomEnvInput+1))
	if err != nil || len(b) > maxRoomEnvInput {
		fmt.Fprintln(os.Stderr, "aiagent room-env-write: input unreadable or too large")
		os.Exit(1)
	}
	var in map[string]string
	if err := json.Unmarshal(b, &in); err != nil {
		fmt.Fprintln(os.Stderr, "aiagent room-env-write: input is not the expected JSON")
		os.Exit(1)
	}
	files := map[string][]byte{}
	for k, v := range in {
		files[k] = []byte(v)
	}
	if err := world.WriteRoomEnvDir(filepath.Join(home, world.RoomEnvSubdir), files); err != nil {
		fmt.Fprintln(os.Stderr, "aiagent room-env-write:", err)
		os.Exit(1)
	}
}

// writePublic writes a root-owned 0644 file through a temp file.
func writePublic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
