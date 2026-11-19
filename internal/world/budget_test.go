package world

import "testing"

func TestAProviderInItsCriticalOrReserveBandAdmitsNoWork(t *testing.T) {
	claude := func(mode string) Quota { return Quota{ProviderID: "anthropic", Mode: mode, Confidence: "actual"} }
	codex := func(mode string) Quota { return Quota{ProviderID: "openai", Mode: mode, Confidence: "actual"} }

	cases := []struct {
		name     string
		quotas   []Quota
		provider string
		ok       bool
	}{
		{"claude normal", []Quota{claude("normal")}, "claude", true},
		{"claude eco", []Quota{claude("eco")}, "claude", true},
		{"claude critical", []Quota{claude("critical")}, "claude", false},
		{"claude reserve", []Quota{claude("reserve")}, "claude", false},
		{"claude guarded (unknown) is not a stop", []Quota{claude("guarded")}, "claude", true},
		{"claude with no reading", nil, "claude", true},
		{"auto skips a critical claude", []Quota{claude("critical"), codex("normal")}, "auto", true},
		{"auto still admits while gemini has no reading", []Quota{claude("critical"), codex("reserve")}, "auto", true},
		{"auto with every provider spent", []Quota{claude("critical"), codex("reserve"), {ProviderID: "google", Mode: "critical", Confidence: "actual"}}, "auto", false},
		{"codex critical does not stop claude", []Quota{codex("critical"), claude("normal")}, "claude", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := admitProvider(c.quotas, c.provider)
			if (err == nil) != c.ok {
				t.Fatalf("admitProvider(%s) err = %v, want ok=%v", c.provider, err, c.ok)
			}
		})
	}
}
