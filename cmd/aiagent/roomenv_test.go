package main

import (
	"os/user"
	"testing"
)

// Only an account osuser.sh made is ever written as: root and this machine's
// own user are not rooms, and neither is a name that does not exist.
func TestRoomAccountRefusesWhatIsNotARoom(t *testing.T) {
	names := []string{"root", "no-such-room-xyz"}
	if u, err := user.Current(); err == nil {
		names = append(names, u.Username)
	}
	for _, n := range names {
		if _, _, _, err := roomAccount(n); err == nil {
			t.Errorf("%s was taken for a room", n)
		}
	}
}
