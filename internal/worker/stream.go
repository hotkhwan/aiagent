// Package worker runs a headless agent CLI for one attempt: its argv, its
// environment, its sandbox and its process group.
package worker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// maxLine caps one stream-json line. A worker that prints more than this in
// one event is not a worker the orchestrator can still trust.
const maxLine = 4 << 20

// Event is one line of a worker's stream, reduced to what the orchestrator
// uses. Kind is one of system, assistant, result, or unknownEvent for anything
// the parser does not know.
type Event struct {
	Kind      string
	Text      string
	IsError   bool
	SessionID string
}

type rawLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// ParseStream calls emit for each line of r. A line that is not JSON, or an
// event type it does not know, becomes an unknownEvent and the stream goes on.
// Only a line over maxLine, or a read error, ends the parse.
func ParseStream(r io.Reader, emit func(Event)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		emit(parseLine(sc.Bytes()))
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("worker stream: %w", err)
	}
	return nil
}

func parseLine(b []byte) Event {
	var l rawLine
	if err := json.Unmarshal(b, &l); err != nil {
		return Event{Kind: "unknownEvent"}
	}
	switch l.Type {
	case "system":
		return Event{Kind: "system", SessionID: l.SessionID}
	case "assistant":
		var text string
		for _, c := range l.Message.Content {
			if c.Type == "text" {
				text += c.Text
			}
		}
		return Event{Kind: "assistant", Text: text, SessionID: l.SessionID}
	case "result":
		return Event{Kind: "result", Text: l.Result, IsError: l.IsError, SessionID: l.SessionID}
	}
	return Event{Kind: "unknownEvent", SessionID: l.SessionID}
}
