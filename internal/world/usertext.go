// internal/world/usertext.go
//
// The one place a user's typed text is checked before it can reach an agent.
// It runs in the gate, before a request is answered here or forwarded to a
// room, so the rule is the same with and without --os-users. userText
// (rooms.go) is the rule itself.
//
// The routes that type a user's own words into an agent's prompt:
//
//	POST /messages                {message}            agcmd dispatch
//	POST /sessions/:id/message    {message}            relay, agcmd, the outbox later
//	POST /sessions/:id/answer     {ask: {answers[].text}} the dialog's free-text option;
//	                              should the dialog close first, the text lands in the prompt
//
// Not here, on purpose: a review answer (/reviews/:id/decision) is sent as a
// reply that always begins with a fixed line the service writes ("✅ ตรวจแล้ว
// …"), so the user's note never starts the prompt; a session name is typed
// after "/rename " and cleaned to one line (names.go).
package world

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// userTextAllowed reports whether a user's request carries only text that may
// be typed into an agent. Requests that carry no typed text pass.
func userTextAllowed(c fiber.Ctx) bool {
	if c.Method() != fiber.MethodPost {
		return true
	}
	path, ok := strings.CutPrefix(c.Path(), apiPrefix)
	if !ok {
		return true
	}
	switch {
	case path == "/messages" || (strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/message")):
		var body struct {
			Message string `json:"message"`
		}
		return json.Unmarshal(c.Body(), &body) == nil && userText(body.Message)
	case strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/answer"):
		var body struct {
			Ask *struct {
				Answers []struct {
					Text string `json:"text"`
				} `json:"answers"`
			} `json:"ask"`
		}
		if json.Unmarshal(c.Body(), &body) != nil {
			return false
		}
		if body.Ask == nil {
			return true // {option: n}: a number, no text
		}
		for _, a := range body.Ask.Answers {
			if a.Text != "" && !userText(a.Text) {
				return false
			}
		}
	}
	return true
}
