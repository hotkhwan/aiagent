package world

import (
	"testing"
	"time"
)

// A finished turn is news too: an agent that was working and is now idle
// tells the person it is done. Each finished turn is its own item (keyed by
// when it ended), so the next one notifies again.
func TestPushItemsIncludeAFinishedTurn(t *testing.T) {
	at := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	idle := Session{SessionID: "aaaa1111", DisplayName: "littledays", Live: true, Status: StatusIdle, LastEventAt: &at}
	items := pushItems(Snapshot{Sessions: []Session{idle}}, nil)
	if len(items) != 1 || items[0].Kind != "done" || items[0].Ref != "aaaa1111" || items[0].Name != "littledays" {
		t.Fatalf("items = %+v", items)
	}
	first := items[0].Key

	later := at.Add(5 * time.Minute)
	idle.LastEventAt = &later
	if next := pushItems(Snapshot{Sessions: []Session{idle}}, nil); next[0].Key == first {
		t.Fatal("a later finished turn must be a new item")
	}

	for name, s := range map[string]Session{
		"still working":       {SessionID: "b1", Live: true, Status: StatusRunning, LastEventAt: &at},
		"not live":            {SessionID: "b2", Live: false, Status: StatusIdle, LastEventAt: &at},
		"never did a turn":    {SessionID: "b3", Live: true, Status: StatusIdle},
		"asking instead":      {SessionID: "b4", Live: true, Status: StatusIdle, LastEventAt: &at, Prompt: &Prompt{Question: "?"}},
		"blocked instead":     {SessionID: "b5", Live: true, Status: StatusIdle, LastEventAt: &at, AgentState: "blocked"},
		"admin's, for a user": {SessionID: "b6", Live: true, Status: StatusIdle, LastEventAt: &at, Protected: true, MessageBlocked: "admin only"},
	} {
		for _, it := range pushItems(Snapshot{Sessions: []Session{s}}, nil) {
			if it.Kind == "done" {
				t.Errorf("%s: no done item expected, got %+v", name, it)
			}
		}
	}
	// a finished background job ("completed") counts as done too
	done := Session{SessionID: "cccc3333", Live: true, Status: StatusCompleted, LastEventAt: &at}
	if items := pushItems(Snapshot{Sessions: []Session{done}}, nil); len(items) != 1 || items[0].Kind != "done" {
		t.Fatalf("completed = %+v", items)
	}
}
