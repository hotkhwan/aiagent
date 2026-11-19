package world

import "context"

// QuotaBudget refuses new work for a provider whose plan is in its critical or
// reserve band. Unknown readings do not stop work; the band is only applied to
// a reading that says the plan is nearly used up.
type QuotaBudget struct{ C *Collector }

// Admit implements orchestrator.Budget.
func (b QuotaBudget) Admit(ctx context.Context, provider string) error {
	return admitProvider(b.C.Snapshot(ctx).Quotas, provider)
}

func admitProvider(qs []Quota, provider string) error {
	order := []string{provider}
	if provider == "" || provider == "auto" {
		order = routableProviders
	}
	for _, p := range order {
		if !spent(qs, providerQuota[p]) {
			return nil
		}
	}
	return ErrConflict("every provider for " + provider + " is out of budget; no new work is started")
}

func spent(qs []Quota, providerID string) bool {
	for _, q := range qs {
		if q.ProviderID == providerID && (q.Mode == "critical" || q.Mode == "reserve") {
			return true
		}
	}
	return false
}
