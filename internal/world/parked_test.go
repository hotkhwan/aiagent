package world

import "testing"

// TestParkedTerminalDoesNotHideItsJob: a terminal whose conversation moved to
// the background keeps the same sessionId in its registry file as the job it
// became. Seen on ata-core: room khwan had the terminal (interactive, parked
// on another job) and the job "khwan" (bg) under 95126f47; the first one read
// won the duplicate, then was dropped as parked, and the job vanished from
// the web and the app.
func TestParkedTerminalDoesNotHideItsJob(t *testing.T) {
	term := Session{SessionID: "95126f47-aaaa", RuntimeType: "claudeCode", Live: true, Kind: "interactive", DisplayName: "khwan", parkedJob: "5a75a2cb"}
	job := Session{SessionID: "95126f47-aaaa", RuntimeType: "claudeCode", Live: true, Kind: "bg", DisplayName: "khwan"}
	other := Session{SessionID: "5a75a2cb-bbbb", RuntimeType: "claudeCode", Live: true, Kind: "bg", DisplayName: "netra cut sop"}
	for _, order := range [][]Session{{other, term, job}, {other, job, term}} {
		var list []Session
		at := map[string]int{}
		for _, s := range order {
			list = keepSession(list, at, s)
		}
		got := dropParked(list)
		if len(got) != 2 {
			t.Fatalf("sessions = %+v", got)
		}
		found := false
		for _, s := range got {
			if s.SessionID == job.SessionID {
				found = s.Kind == "bg" && s.parkedJob == ""
			}
		}
		if !found {
			t.Fatalf("the job was hidden by its parked terminal: %+v", got)
		}
	}
}
