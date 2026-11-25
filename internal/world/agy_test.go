package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgyScreens(t *testing.T) {
	if agyBusy("Welcome\n>\n? for shortcuts   Gemini") || !agyBusy("Thinking...\nesc to interrupt") {
		t.Error("busy is wrong")
	}
	if !onDialog("Run this command?\n> 1. Yes\n  ↑/↓ Navigate · tab Amend\nesc to cancel    Gemini 4") {
		t.Error("a permission question was not seen")
	}
	if onDialog("answer mentions esc to cancel\nmore\nlines\n>\n────\n? for shortcuts") {
		t.Error("the idle prompt was taken for a question")
	}
}

func TestGoogleQuotaNeedsASignInAndAReading(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if q := e.col.googleQuotas(now); len(q) != 0 {
		t.Fatalf("signed out: %v", q)
	}
	if err := os.MkdirAll(filepath.Dir(e.col.agyTokenPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(e.col.agyTokenPath(), []byte("x"), 0o600)
	if q := e.col.googleQuotas(now); len(q) != 1 || q[0].Confidence != "unknown" {
		t.Fatalf("no reading: %v", q)
	}
	_ = os.WriteFile(filepath.Join(e.col.serviceHome(), ".gemini", usageFile), []byte(`{"observedAt":"2026-01-01T23:00:00Z","fiveHour":{"usedPercent":12,"resetsAt":"2026-01-01T23:30:00Z"},"sevenDay":{"usedPercent":34,"resetsAt":"2026-01-05T00:00:00Z"}}`), 0o600)
	q := e.col.googleQuotas(now)
	if len(q) != 2 || q[0].Confidence != "stale" || q[1].Confidence != "actual" || *q[1].UsedPercent != 34 {
		t.Fatalf("%+v", q)
	}
}
