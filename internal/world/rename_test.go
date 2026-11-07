package world

import (
	"strings"
	"testing"
)

// TestCleanSessionName pins the rules for a name that will be typed into the
// CLI after /rename. A newline folds to a space because it would submit the
// command early, and a leading '-' is refused because it reads as a flag.
func TestCleanSessionName(t *testing.T) {
	ok := map[string]string{
		"  ตรวจ repo   LittleDays  ": "ตรวจ repo LittleDays",
		"two\nlines":                 "two lines",
		"plain":                      "plain",
	}
	for in, want := range ok {
		got, err := cleanSessionName(in)
		if err != nil || got != want {
			t.Errorf("cleanSessionName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	refused := []string{
		"",
		"   ",
		"-rf",
		"bad\x07bell",
		strings.Repeat("ก", maxSessionNameRunes+1),
		"\xff\xfe",
	}
	for _, in := range refused {
		if got, err := cleanSessionName(in); err == nil {
			t.Errorf("cleanSessionName(%q) accepted as %q", in, got)
		}
	}

	// Exactly the limit is allowed.
	if _, err := cleanSessionName(strings.Repeat("ก", maxSessionNameRunes)); err != nil {
		t.Errorf("name at the limit refused: %v", err)
	}
}
