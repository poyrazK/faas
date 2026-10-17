package functionmigration

// adr: 1353

import "errors"

type CoordinatorIdentity struct {
	PID       int    `json:"pid"`
	Start     string `json:"start"`
	BinarySHA string `json:"binary_sha256"`
}

type CoordinatorEvent struct {
	Phase           string `json:"phase"`
	Completed       int    `json:"completed,omitempty"`
	Seeded          bool   `json:"seeded,omitempty"`
	DiscoveredPhase string `json:"discovered_phase,omitempty"`
}

type RestartEvidence struct {
	Before           CoordinatorIdentity `json:"before"`
	After            CoordinatorIdentity `json:"after"`
	SIGKILLVerified  bool                `json:"sigkill_verified"`
	LeaseRetained    bool                `json:"source_lease_retained_at_crash"`
	PendingRetained  bool                `json:"pending_destination_retained_at_crash"`
	CommittedAtCrash bool                `json:"committed_at_crash"`
	ExecutionClosed  bool                `json:"execution_closed_at_crash"`
	SourceAtCrash    Owner               `json:"source_at_crash"`
	DestinationAfter Owner               `json:"destination_after_startup"`
	SQLAfter         []Session           `json:"sql_after_startup"`
	Startup          CoordinatorEvent    `json:"startup"`
}

func (r Run) checkRestartEvidence() error {
	e := r.Restart
	if e == nil {
		return errors.New("coordinator restart lacks observed process loss")
	}
	for _, p := range []CoordinatorIdentity{e.Before, e.After} {
		if p.PID <= 0 || !processStart(p.Start) || !hexBytes(p.BinarySHA, 32) {
			return errors.New("missing coordinator process or executable identity")
		}
	}
	if e.Before.PID == e.After.PID && e.Before.Start == e.After.Start || e.Before.BinarySHA != e.After.BinarySHA || !e.SIGKILLVerified || !e.LeaseRetained || !e.ExecutionClosed ||
		e.SourceAtCrash != r.Source || e.DestinationAfter != r.Destination || !SameSessions(e.SQLAfter, r.DestinationSQL) ||
		e.Startup.Phase != "recovered" || e.Startup.Completed != 1 || !e.Startup.Seeded {
		return errors.New("coordinator startup changed ownership, SQL or omitted durable recovery")
	}
	expected := "ready"
	if r.Scenario == RestartCommitted {
		expected = "committed"
	}
	if e.Startup.DiscoveredPhase != expected || e.CommittedAtCrash != (r.Scenario == RestartCommitted) || e.PendingRetained != (r.Scenario == RestartReady) {
		return errors.New("coordinator was not killed at the required durable boundary")
	}
	return nil
}
