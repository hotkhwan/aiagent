package web

import (
	"strings"
	"testing"
)

// A session that has stopped (after a reboot, say) must offer Resume, and
// Resume must call the resume endpoint, not the message endpoint.
func TestStoppedSessionOffersResume(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "sessionHeader"), "doResume(sess)") {
		t.Errorf("the session header must offer Resume for a session that is not running")
	}
	resume := jsFunc(t, app, "doResume")
	if !strings.Contains(resume, "'/sessions/' + encodeURIComponent(sess.sessionId) + '/resume'") {
		t.Errorf("doResume must POST to /sessions/:id/resume")
	}
	if strings.Contains(resume, "/message") {
		t.Errorf("doResume must not send a message")
	}
}
