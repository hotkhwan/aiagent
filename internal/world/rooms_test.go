package world

import (
	"testing"
	"time"
)

func TestRoomModelFollowsWeeklyUsage(t *testing.T) {
	p := RoomPolicy{Enabled: true, WeeklyPct: 80}
	for _, c := range []struct {
		weekly float64
		known  bool
		cur    string
		want   string
	}{
		{80, true, roomMainModel, roomMainModel}, // "more than", not "at"
		{81, true, roomMainModel, roomLowModel},
		{78, true, roomLowModel, roomLowModel}, // no flapping on the line
		{74, true, roomLowModel, roomMainModel},
		{99, false, roomLowModel, roomMainModel}, // never guessed
	} {
		if got := roomModel(p, c.weekly, c.known, c.cur); got != c.want {
			t.Errorf("%+v = %s", c, got)
		}
	}
	if roomModel(RoomPolicy{WeeklyPct: 80}, 99, true, "") != roomMainModel {
		t.Error("a disabled rule switched")
	}
}

func TestRoomRestartsOnceAndOnlyWhenNeeded(t *testing.T) {
	at := func(m int) *time.Time { x := time.Unix(int64(m)*60, 0); return &x }
	chg := *at(10)
	for _, c := range []struct {
		name string
		s    Session
		want bool
	}{
		{"started before the change", Session{Live: true, StartedAt: at(5)}, true},
		{"restarted, last reply still the old model", Session{Live: true, StartedAt: at(11), LastEventAt: at(9), Model: "claude-opus-5-5"}, false},
		{"answered on another model after start", Session{Live: true, StartedAt: at(11), LastEventAt: at(12), Model: "claude-opus-5-5"}, true},
		{"right model", Session{Live: true, StartedAt: at(11), LastEventAt: at(12), Model: "claude-haiku-5-5"}, false},
		{"ended", Session{StartedAt: at(5)}, false},
	} {
		if got := needsModel(c.s, roomLowModel, chg); got != c.want {
			t.Errorf("%s = %v", c.name, got)
		}
	}
}

func TestUserCannotTypeModelCommands(t *testing.T) {
	for _, m := range []string{"/model", "/model haiku", " /model sonnet", "\u200b/model x", "\x01/mod", "/m", "/config", "/fast", "/odel", "/", "/settings\nx", "/login", "/compactx", "/model/x"} {
		if userText(m) {
			t.Errorf("%q was let through", m)
		}
	}
	for _, m := range []string{"สวัสดี", "ใช้ /model ได้ไหม", "/compact", "/rename งานใหม่", "/home/x/docs อ่านให้หน่อย"} {
		if !userText(m) {
			t.Errorf("%q was blocked", m)
		}
	}
}
