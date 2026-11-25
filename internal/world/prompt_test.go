package world

import "testing"

const askScreen = ` Ran 2 shell commands · 1 failed
────────────────────────────────────────────────────────────
 ☐ ที่ทำงาน
│ Brief สั่งให้สร้าง repo ใหม่ แต่มี repo อยู่แล้ว
│ จะให้ทำที่ไหนครับ?
❯ 1. clone repo เดิม (แนะนำ)
     git clone แล้วทำบน branch feat/landing
  2. repo ใหม่ตาม brief
     git init แยก
  3. Type something.
────────────────────────────────────────────────────────────
  4. Chat about this
Enter to select · ↑/↓ to navigate · Esc to cancel
`

func TestParsePromptReadsTheQuestionAndItsChoices(t *testing.T) {
	p := parsePrompt(askScreen)
	if p == nil || len(p.Options) != 4 {
		t.Fatalf("%+v", p)
	}
	if p.Question != "ที่ทำงาน Brief สั่งให้สร้าง repo ใหม่ แต่มี repo อยู่แล้ว จะให้ทำที่ไหนครับ?" {
		t.Errorf("question = %q", p.Question)
	}
	o := p.Options
	if !o[0].Current || o[1].Current || o[0].Label != "clone repo เดิม (แนะนำ)" || o[0].Detail != "git clone แล้วทำบน branch feat/landing" {
		t.Errorf("first = %+v", o[0])
	}
	if o[1].NeedsText || !o[2].NeedsText || !o[3].NeedsText {
		t.Errorf("needsText = %v %v %v", o[1].NeedsText, o[2].NeedsText, o[3].NeedsText)
	}
}

func TestParsePromptIgnoresAListThatIsNotAQuestion(t *testing.T) {
	for _, s := range []string{
		"แผนคือ\n  1. เพิ่มปุ่ม\n  2. เขียน test\n❯ \n────────\n  ? for shortcuts",
		"Do you want to proceed?\n❯ 1. Yes\nEsc to cancel", // one choice is not a list
		"",
	} {
		if p := parsePrompt(s); p != nil {
			t.Errorf("%q parsed as %+v", s, p)
		}
	}
	perm := "Bash command\n  ls /x\nDo you want to proceed?\n❯ 1. Yes\n  2. Yes, allow reading from /x\n  3. No\n\nEsc to cancel · Tab to amend"
	if p := parsePrompt(perm); p == nil || len(p.Options) != 3 || p.Options[2].Label != "No" {
		t.Errorf("permission prompt = %+v", p)
	}
}
