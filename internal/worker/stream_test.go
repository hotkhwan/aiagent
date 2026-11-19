package worker

import (
	"os"
	"strings"
	"testing"
)

// The fixture is hand-written in claude 2.1.325's stream-json shape. It holds
// an event type nobody has taught the parser, and a line that is not JSON.
func TestParseStreamKeepsUnknownEventsAndReadsTheResult(t *testing.T) {
	f, err := os.Open("testdata/claude_stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got []Event
	if err := ParseStream(f, func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("a stream with unknown events must not fail: %v", err)
	}
	kinds := make([]string, len(got))
	for i, e := range got {
		kinds[i] = e.Kind
	}
	want := []string{"system", "assistant", "unknownEvent", "unknownEvent", "result"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	last := got[len(got)-1]
	if last.IsError || !strings.Contains(last.Text, "3f2a1c9") || last.SessionID == "" {
		t.Fatalf("result event = %+v", last)
	}
	if got[1].Text != "ขอดูไฟล์ outbox ก่อน" {
		t.Fatalf("assistant text = %q", got[1].Text)
	}
}

func TestParseStreamRejectsAnOversizedLine(t *testing.T) {
	huge := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Repeat("x", maxLine+1) + `"}]}}`
	err := ParseStream(strings.NewReader(huge+"\n"), func(Event) {})
	if err == nil {
		t.Fatal("a line over the cap must stop the parse, not grow without bound")
	}
}
