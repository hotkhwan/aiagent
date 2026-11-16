package planner

import (
	"strings"
	"testing"
)

func goodPlan() Plan {
	return Plan{
		Reply: "เสนอเป็น 2 งาน",
		Proposals: []Proposal{
			{Title: "แก้ outbox ไม่ให้ส่งซ้ำหลัง crash", ProjectID: "aiagent", Kind: "bug", Provider: "claude",
				ProviderReason: "งานแก้ Go ในโค้ดเดิม", AcceptanceCriteria: []string{"restart แล้วไม่ส่งซ้ำ"}},
			{Title: "ยืนยันการรับข้อความ (ack)", ProjectID: "aiagent", Kind: "feature", Provider: "auto",
				AcceptanceCriteria: []string{"บันทึกสถานะ ack"}, DependsOn: []int{1}, RiskLabels: []string{"deploy"}},
		},
	}
}

var planContext = PlanInput{
	Request:  "อยากให้ส่งข้อความถึง agent ได้แน่นอน และไม่ส่งซ้ำ",
	Projects: []string{"aiagent"},
	Tasks:    []TaskBrief{{ID: "T-0003", Title: "เดิม", Status: "running", ProjectID: "aiagent"}},
}

// What the model returns is a proposal, never an instruction: it is accepted
// only when every field is something the task store could hold.
func TestCheckPlanAcceptsAWellFormedPlan(t *testing.T) {
	p := goodPlan()
	if err := p.Check(planContext); err != nil {
		t.Fatalf("a well-formed plan was refused: %v", err)
	}
	if p.Proposals[1].Provider != "" {
		t.Errorf(`provider "auto" should be stored as "" (routing chooses), got %q`, p.Proposals[1].Provider)
	}
}

func TestCheckPlanRefusesWhatTheStoreCouldNotHold(t *testing.T) {
	long := strings.Repeat("ก", 101) // 303 bytes: the store's title limit is 300
	cases := map[string]func(*Plan){
		"nothing at all":              func(p *Plan) { *p = Plan{} },
		"a project it made up":        func(p *Plan) { p.Proposals[0].ProjectID = "billing" },
		"a project that is a path":    func(p *Plan) { p.Proposals[0].ProjectID = "../etc" },
		"an unknown kind":             func(p *Plan) { p.Proposals[0].Kind = "epic" },
		"an unknown provider":         func(p *Plan) { p.Proposals[0].Provider = "gpt" },
		"no acceptance criteria":      func(p *Plan) { p.Proposals[0].AcceptanceCriteria = nil },
		"a blank criterion":           func(p *Plan) { p.Proposals[0].AcceptanceCriteria = []string{"  "} },
		"an empty title":              func(p *Plan) { p.Proposals[0].Title = " " },
		"a title past the limit":      func(p *Plan) { p.Proposals[0].Title = long },
		"a dependency on itself":      func(p *Plan) { p.Proposals[1].DependsOn = []int{2} },
		"a dependency on a later one": func(p *Plan) { p.Proposals[0].DependsOn = []int{2} },
		"a dependency out of range":   func(p *Plan) { p.Proposals[1].DependsOn = []int{0} },
		"a task that does not exist":  func(p *Plan) { p.Proposals[0].BlockedBy = []string{"T-9999"} },
		"an unknown risk label":       func(p *Plan) { p.Proposals[0].RiskLabels = []string{"spicy"} },
		"an absolute allowed path":    func(p *Plan) { p.Proposals[0].AllowedPaths = []string{"/etc/**"} },
		"an allowed path that climbs": func(p *Plan) { p.Proposals[0].AllowedPaths = []string{"../other/**"} },
		"too many proposals": func(p *Plan) {
			for len(p.Proposals) <= maxProposals {
				p.Proposals = append(p.Proposals, p.Proposals[0])
			}
		},
		"too many questions": func(p *Plan) { p.Questions = []string{"a", "b", "c", "d", "e", "f"} },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			p := goodPlan()
			breakIt(&p)
			if err := p.Check(planContext); err == nil {
				t.Fatalf("a plan with %s was accepted", name)
			}
		})
	}
}

// With no project known yet the PM may name one, but only as a plain slug.
func TestCheckPlanAllowsANewProjectOnlyWhenNoneIsKnown(t *testing.T) {
	p := goodPlan()
	p.Proposals[0].ProjectID, p.Proposals[1].ProjectID = "shop-api", "shop-api"
	in := planContext
	in.Projects = nil
	if err := p.Check(in); err != nil {
		t.Fatalf("a slug project with none known was refused: %v", err)
	}
}

// A question on its own is a complete answer: the PM asks instead of guessing.
func TestCheckPlanAcceptsQuestionsAlone(t *testing.T) {
	p := Plan{Reply: "ขอข้อมูลเพิ่ม", Questions: []string{"ข้อความที่ส่งไม่สำเร็จควรแสดงที่ไหน?"}}
	if err := p.Check(planContext); err != nil {
		t.Fatalf("questions alone were refused: %v", err)
	}
}

// The reviewer's note is advice for the person deciding, so it carries no
// verdict to obey — only a summary and things worth a look.
func TestCheckAdvice(t *testing.T) {
	good := Advice{Summary: "แก้ outbox ให้จำสถานะส่งแล้ว", Concerns: []Concern{{Severity: "warn", File: "internal/world/outbox.go", Note: "ไม่มี test กรณี crash"}}}
	if err := good.Check(); err != nil {
		t.Fatalf("well-formed advice was refused: %v", err)
	}
	for name, a := range map[string]Advice{
		"no summary":          {},
		"an unknown severity": {Summary: "x", Concerns: []Concern{{Severity: "blocker", Note: "n"}}},
		"an empty concern":    {Summary: "x", Concerns: []Concern{{Severity: "info"}}},
	} {
		if err := a.Check(); err == nil {
			t.Errorf("advice with %s was accepted", name)
		}
	}
}
