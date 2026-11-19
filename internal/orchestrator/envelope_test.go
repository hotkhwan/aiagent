package orchestrator

import (
	"strings"
	"testing"

	"github.com/hotkhwan/aiagent/internal/taskstore"
)

func TestHeadlessEnvelopeDoesNotAskTheWorkerToCreateItsBranchAgain(t *testing.T) {
	task := taskstore.Task{ID: "T-0001", NewTask: taskstore.NewTask{Title: "Add a file"}, Revision: 1}
	a := taskstore.Attempt{ID: "T-0001-A1", Branch: "aiagent/T-0001-A1", BaseCommit: "abc123"}

	msg := HeadlessEnvelope(task, a)
	if strings.Contains(msg, "git switch -c") {
		t.Fatalf("headless envelope tells the worker to create a branch that already exists:\n%s", msg)
	}
	for _, want := range []string{"already on branch aiagent/T-0001-A1", "abc123", ".aiagent/results/T-0001-A1.json"} {
		if !strings.Contains(msg, want) {
			t.Errorf("headless envelope lacks %q:\n%s", want, msg)
		}
	}
}

func TestEnvelopeKeepsBranchCreationForTheTmuxPath(t *testing.T) {
	task := taskstore.Task{ID: "T-0001", NewTask: taskstore.NewTask{Title: "Add a file"}, Revision: 1}
	a := taskstore.Attempt{ID: "T-0001-A1", Branch: "aiagent/T-0001-A1", BaseCommit: "abc123"}

	if !strings.Contains(Envelope(task, a), "git switch -c aiagent/T-0001-A1 abc123") {
		t.Fatal("tmux envelope lost its branch instruction")
	}
}
