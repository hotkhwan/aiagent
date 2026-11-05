// internal/aiagent/watches.go
//
// Background watches a Claude Code session keeps (the Monitor tool), e.g. a
// Jenkins build it started and is waiting on. Only the agent's own one-line
// description and the latest event line leave the host; the watch command
// (which may carry credentials) is inspected for a CI shape and dropped.
package world

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Watch is one background watch of a session.
type Watch struct {
	Label     string    `json:"label"`
	Event     string    `json:"event,omitempty"`
	CI        bool      `json:"ci"`
	StartedAt time.Time `json:"startedAt"`
	EventAt   time.Time `json:"eventAt"`
	// OK is set on a finished CI watch: true when its last event reads as a
	// success, false as a failure, nil when unclear.
	OK *bool `json:"ok,omitempty"`
}

// lastCIKeep: how long a finished CI watch stays visible (for the rocket).
const lastCIKeep = 15 * time.Minute

var (
	ciOKRE   = regexp.MustCompile(`(?i)\b(success|succeeded|passed|deployed)\b|✅|=> ?ok\b`)
	ciFailRE = regexp.MustCompile(`(?i)\b(fail(ed|ure)?|error|aborted|unstable)\b|❌`)
)

type watchState struct {
	Watch
	taskID  string
	expires time.Time
	ended   bool
}

const (
	watchDefaultTTL = 30 * time.Minute
	watchMaxTTL     = 2 * time.Hour
	watchMax        = 6
)

var (
	ciShape     = regexp.MustCompile(`(?i)jenkins|/job/|queue/item|/ci\b|wfapi|argocd|\bbuild\b|\bdeploy|candidate|\bprod-`)
	notifTaskRE = regexp.MustCompile(`<task-id>([^<]+)</task-id>`)
	notifStatRE = regexp.MustCompile(`<status>([^<]+)</status>`)
	notifEvtRE  = regexp.MustCompile(`(?s)<event>(.*?)</event>`)
)

// updateWatches folds Monitor starts, their task ids and task notifications
// from transcript lines into st.watches (keyed by tool_use id).
func updateWatches(lines [][]byte, st *transcriptState) {
	if st.watches == nil {
		st.watches = map[string]*watchState{}
	}
	byTask := map[string]*watchState{}
	for _, w := range st.watches {
		if w.taskID != "" {
			byTask[w.taskID] = w
		}
	}
	for _, raw := range lines {
		hasMon := bytes.Contains(raw, []byte(`"name":"Monitor"`))
		hasTask := bytes.Contains(raw, []byte(`"taskId"`))
		hasNote := bytes.Contains(raw, []byte(`task-notification`))
		if !hasMon && !hasTask && !hasNote {
			continue
		}
		var ln struct {
			Type          string `json:"type"`
			Timestamp     string `json:"timestamp"`
			IsSidechain   bool   `json:"isSidechain"`
			ToolUseResult *struct {
				TaskID    string `json:"taskId"`
				TimeoutMs int64  `json:"timeoutMs"`
			} `json:"toolUseResult"`
			Message *struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &ln) != nil || ln.Message == nil || ln.IsSidechain {
			continue
		}
		at := parseTS(ln.Timestamp)
		var text string
		var blocks []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Text      string          `json:"text"`
		}
		if json.Unmarshal(ln.Message.Content, &text) != nil {
			_ = json.Unmarshal(ln.Message.Content, &blocks)
		}
		for _, b := range blocks {
			switch {
			case ln.Type == "assistant" && b.Type == "tool_use" && b.Name == "Monitor" && b.ID != "":
				var in struct {
					Description string `json:"description"`
					Command     string `json:"command"`
					TimeoutMs   int64  `json:"timeout_ms"`
				}
				_ = json.Unmarshal(b.Input, &in)
				ttl := time.Duration(in.TimeoutMs) * time.Millisecond
				if ttl <= 0 {
					ttl = watchDefaultTTL
				}
				if ttl > watchMaxTTL {
					ttl = watchMaxTTL
				}
				if _, ok := st.watches[b.ID]; !ok {
					label := truncateRunes(Redact(strings.TrimSpace(in.Description)), 120)
					if label == "" {
						label = "Monitor"
					}
					st.watches[b.ID] = &watchState{Watch: Watch{Label: label, CI: ciShape.MatchString(in.Description) || ciShape.MatchString(in.Command), StartedAt: at}, expires: at.Add(ttl)}
				}
			case b.Type == "tool_result" && b.ToolUseID != "" && ln.ToolUseResult != nil && ln.ToolUseResult.TaskID != "":
				if w, ok := st.watches[b.ToolUseID]; ok && w.taskID == "" {
					w.taskID = ln.ToolUseResult.TaskID
					byTask[w.taskID] = w
				}
			case b.Type == "text" && strings.Contains(b.Text, "<task-notification>"):
				text += "\n" + b.Text
			}
		}
		if !strings.Contains(text, "<task-notification>") {
			continue
		}
		for _, note := range strings.Split(text, "<task-notification>")[1:] {
			m := notifTaskRE.FindStringSubmatch(note)
			if m == nil {
				continue
			}
			w := byTask[strings.TrimSpace(m[1])]
			if w == nil {
				continue
			}
			// some copies of a notification (e.g. re-delivered inside a later
			// turn) carry no timestamp; never let them rewind the event
			if e := notifEvtRE.FindStringSubmatch(note); e != nil && (at.IsZero() || !at.Before(w.EventAt)) {
				w.Event = truncateRunes(Redact(htmlUnescape(strings.TrimSpace(e[1]))), 160)
				if !at.IsZero() {
					w.EventAt = at
				}
			}
			if s := notifStatRE.FindStringSubmatch(note); s != nil {
				w.ended = true
			}
		}
	}
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&gt;", ">", "&lt;", "<", "&amp;", "&", "&quot;", `"`, "&#39;", "'").Replace(s)
}

// activeWatches returns the watches still running at now, newest first, and
// forgets the finished ones.
func activeWatches(st *transcriptState, now time.Time) []Watch {
	var out []Watch
	for id, w := range st.watches {
		if w.ended && w.CI {
			done := w.Watch
			switch {
			case ciFailRE.MatchString(done.Event):
				f := false
				done.OK = &f
			case ciOKRE.MatchString(done.Event):
				t := true
				done.OK = &t
			}
			if st.lastCI == nil || !done.EventAt.Before(st.lastCI.EventAt) {
				st.lastCI = &done
			}
		}
		if w.ended || now.After(w.expires) {
			delete(st.watches, id)
			continue
		}
		out = append(out, w.Watch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > watchMax {
		out = out[:watchMax]
	}
	return out
}
