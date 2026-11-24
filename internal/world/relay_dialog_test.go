package world

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Screens captured from real relay panes on 2026-11-23. Each is a question or
// menu Claude Code drew over its input box, with a ❯ beside one option. The
// relay took that ❯ for the prompt, typed the message and pressed Enter into
// the dialog, and reported the send as ok (owner: "บางหน้ากดส่งแล้วแต่ไม่ไป").
// Enter there also picks the highlighted option for the operator.
var dialogScreens = map[string]string{
	"teach auto mode": `  result: session "aiagent repo" รายงานว่าทำปุ่ม Resume เสร็จ
✻ Churned for 18s · done 7:43 PM
▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔
   Teach auto mode about your environment?
   Claude Code reads this project, your recent Claude sessions, and optionally your shell history and other repositories.
     How you use Claude here     Mixed
   ❯ Also scan shell history     true
     Also scan your other repos  false
     Continue
   ←/→ to change · Enter to continue · Esc to cancel`,
	"rewind menu": `   Restore the code and/or conversation to the point before…
     ↑ 16 more
     ทำตอ่ได้เลย …
     No code changes
     ตอนนี้เหลืองานค้างอะไรไหม
     ⚠ No code restore
   ❯ (current)
   Enter to continue · Esc to cancel`,
	// Captured 2026-11-24 from a relay whose CLI had gone back to the agent
	// view. The ❯ there is the box that dispatches a NEW session, so a message
	// typed into it went to a different CLI than the one the operator picked
	// (owner: "บางทีสั่งหน้า web มาส่ง cli คนละตัว").
	"agent view": ` ✽ Global PM                   New review page is filed
Completed
 ✻ wobbloo mobile puzzle game  สินค้าในแอปของ WOBBLOO บน Google Play สร้างครบ 5 ตัว
 ∙ littledays DLC v1           Open in a terminal · continue it there
────────────────────────────────
❯ describe a task for a new session
────────────────────────────────
  ⏵⏵ auto mode · enter to collapse · ctrl+x to remove all · ? for shortcuts`,
}

func TestSendRefusesToTypeIntoADialog(t *testing.T) {
	for name, screen := range dialogScreens {
		t.Run(name, func(t *testing.T) {
			e := doneJobEnv(t)
			e.runner.dialogScreen = screen
			_, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน")
			if err == nil {
				t.Fatalf("send reported ok while the session was showing a dialog")
			}
			if !strings.Contains(err.Error(), "aiagent-bbbb2222") {
				t.Errorf("err = %v, want it to name the tmux session to answer the dialog in", err)
			}
			for _, c := range e.runner.callsTo("tmux") {
				if c[0] == "send-keys" {
					t.Fatalf("nothing may be typed into a dialog, Enter would pick an option: %v", c)
				}
			}
		})
	}
}

// Past user messages are drawn with a ❯ too, and the idle prompt sits under
// them. That screen is ready and must still be sent to.
func TestSendStillTypesAtAnIdlePromptWithHistory(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.dialogScreen = `❯ ทำตอ่ได้เลย
⏺ You're telling me to keep going.
────────────────────────────────
❯
────────────────────────────────
  Opus 5.5 · 7d 12%
  ⏵⏵ auto mode on (shift+tab to cycle)`
	if _, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send at an idle prompt: %v", err)
	}
	typed := false
	for _, c := range e.runner.callsTo("tmux") {
		if c[0] == "send-keys" && slices.Contains(c, "ตรวจแล้ว ผ่าน") {
			typed = true
		}
	}
	if !typed {
		t.Fatalf("the message was not typed at the idle prompt")
	}
}
