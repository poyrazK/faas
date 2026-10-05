// adr: 585
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type serviceConnectionRecoveryProvider struct {
	serviceMaintenanceProvider
	abandons, observes int
	maintenance        CheckpointMaintenance
	identity           CheckpointConnectionIdentity
	fault              string
}

func (p *serviceConnectionRecoveryProvider) recoveryResult(ctx context.Context, m CheckpointMaintenance, i CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error) {
	_, p.deadline = ctx.Deadline()
	p.maintenance, p.identity = m, i
	o := CheckpointConnectionTerminal{CheckpointConnectionIdentity: i, State: "abandoned", ReleasedAt: time.Now().UTC().Truncate(time.Microsecond)}
	switch p.fault {
	case "owner":
		o.OwnerToken = uuid.NewString()
	case "source":
		o.SourceResourceID += "/other"
	case "state":
		o.State = "closed"
	case "missing_time":
		o.ReleasedAt = time.Time{}
	case "missing_record":
		return CheckpointConnectionTerminal{}, ErrNotFound
	}
	return o, nil
}

func (p *serviceConnectionRecoveryProvider) AbandonCheckpointConnections(ctx context.Context, definition RestoreSourceDefinition, m CheckpointMaintenance, i CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error) {
	p.abandons++
	p.spec = definition.Spec
	p.definition = definition
	return p.recoveryResult(ctx, m, i)
}

func (p *serviceConnectionRecoveryProvider) ObserveCheckpointConnections(ctx context.Context, definition RestoreSourceDefinition, m CheckpointMaintenance, i CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error) {
	p.observes++
	p.spec = definition.Spec
	p.definition = definition
	return p.recoveryResult(ctx, m, i)
}

func TestCheckpointConnectionServiceAuthenticatesSeparateOwnersAndTerminalRecords(t *testing.T) {
	p := &serviceConnectionRecoveryProvider{serviceMaintenanceProvider: serviceMaintenanceProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}}}
	r := testRegistry(t, p, nil)
	s := testService(t, r, NewMemoryStore())
	b, err := r.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	d := RestoreSourceDefinition{Spec: testSpec(), BackendID: b.ID, BackendFingerprint: b.Fingerprint, ProviderResourceID: "project", DataResourceID: "project/branch"}
	m := CheckpointMaintenance{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, State: "ready", OwnerOID: 10101, DatabaseOID: 20202}
	i := CheckpointConnectionIdentity{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID}
	for _, call := range []func(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error){s.AbandonCheckpointConnections, s.ObserveCheckpointConnections} {
		actual, err := call(t.Context(), d, m, i)
		if err != nil || actual.CheckpointConnectionIdentity != i || actual.State != "abandoned" || p.definition != d || p.maintenance != m || p.identity != i || !p.deadline {
			t.Fatalf("owned recovery: %+v %v", actual, err)
		}
		for _, fault := range []string{"owner", "same_owner", "source", "maintenance_source", "maintenance_state", "missing_oid", "backend", "fingerprint", "region"} {
			definition, maintenance, identity := d, m, i
			switch fault {
			case "owner":
				identity.OwnerToken = uuid.Nil.String()
			case "same_owner":
				identity.OwnerToken = m.OwnerToken
			case "source":
				identity.SourceResourceID += "/other"
			case "maintenance_source":
				maintenance.SourceResourceID += "/other"
			case "maintenance_state":
				maintenance.State = "reserved"
			case "missing_oid":
				maintenance.DatabaseOID = 0
			case "backend":
				definition.BackendID = "other"
			case "fingerprint":
				definition.BackendFingerprint = "changed"
			case "region":
				definition.Spec.Region = "eu-central-1"
			}
			before := p.abandons + p.observes
			if _, err := call(t.Context(), definition, maintenance, identity); err == nil || p.abandons+p.observes != before {
				t.Fatalf("invalid %s reached provider: %v", fault, err)
			}
		}
		for _, fault := range []string{"owner", "source", "state", "missing_time", "missing_record"} {
			p.fault = fault
			want := ErrConflict
			if fault == "missing_record" {
				want = ErrNotFound
			}
			if actual, err := call(t.Context(), d, m, i); !errors.Is(err, want) || actual != (CheckpointConnectionTerminal{}) {
				t.Fatalf("accepted %s terminal: %+v %v", fault, actual, err)
			}
		}
		p.fault = ""
	}
}
