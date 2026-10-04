// adr: 583
package managedpostgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type serviceMaintenanceProvider struct {
	fakeProvider
	calls      int
	last       CheckpointMaintenanceRequest
	spec       Spec
	definition RestoreSourceDefinition
	deadline   bool
	fault      string
}

func (p *serviceMaintenanceProvider) ReconcileCheckpointMaintenance(ctx context.Context, definition RestoreSourceDefinition, r CheckpointMaintenanceRequest) (CheckpointMaintenance, error) {
	p.calls++
	p.last, p.spec = r, definition.Spec
	p.definition = definition
	_, p.deadline = ctx.Deadline()
	o := CheckpointMaintenance{OwnerToken: r.OwnerToken, SourceResourceID: r.SourceResourceID, State: "reserved", OwnerOID: 10101}
	if r.Phase != "role" {
		o.DatabaseOID = 20202
	}
	if r.Phase == "activation" || r.Phase == "ready" {
		o.State = "ready"
	}
	switch p.fault {
	case "token":
		o.OwnerToken = uuid.NewString()
	case "source":
		o.SourceResourceID += "/other"
	case "owner_oid":
		o.OwnerOID++
	case "database_oid":
		o.DatabaseOID++
	case "missing_owner":
		o.OwnerOID = 0
	case "missing_database":
		o.DatabaseOID = 0
	case "state":
		o.State = "retired"
	}
	return o, nil
}

func maintenanceServiceFixture(t *testing.T) (*Service, *serviceMaintenanceProvider, RestoreSourceDefinition) {
	t.Helper()
	p := &serviceMaintenanceProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}}
	r := testRegistry(t, p, nil)
	s := testService(t, r, NewMemoryStore())
	b, err := r.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, RestoreSourceDefinition{Spec: testSpec(), BackendID: b.ID, BackendFingerprint: b.Fingerprint,
		ProviderResourceID: "project", DataResourceID: "project/branch"}
}

func TestCheckpointMaintenanceServiceUsesFrozenBackendAndSpec(t *testing.T) {
	s, p, d := maintenanceServiceFixture(t)
	// Recovery of an owned source hold continues while new provisioning is off.
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	for _, phase := range []string{"role", "database", "activation", "ready"} {
		r := CheckpointMaintenanceRequest{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, Phase: phase}
		if phase != "role" {
			r.OwnerOID = 10101
		}
		if phase == "activation" || phase == "ready" {
			r.DatabaseOID = 20202
		}
		actual, err := s.ReconcileCheckpointMaintenance(t.Context(), d, r)
		if err != nil || !checkpointMaintenanceMatches(r, actual) || p.last != r || p.definition != d || !p.deadline {
			t.Fatalf("%s: %+v %v", phase, actual, err)
		}
	}
	for _, fault := range []string{"backend", "fingerprint", "source", "region", "spec", "invalid_owner", "phase", "missing_owner_oid", "missing_database_oid"} {
		t.Run(fault, func(t *testing.T) {
			definition := d
			r := CheckpointMaintenanceRequest{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, Phase: "ready", OwnerOID: 10101, DatabaseOID: 20202}
			switch fault {
			case "backend":
				definition.BackendID = "missing"
			case "fingerprint":
				definition.BackendFingerprint = "changed"
			case "source":
				definition.DataResourceID += "/other"
			case "region":
				definition.Spec.Region = "eu-central-1"
			case "spec":
				definition.Spec.PostgresMajor = 99
			case "invalid_owner":
				r.OwnerToken = uuid.Nil.String()
			case "phase":
				r.Phase = "delete"
			case "missing_owner_oid":
				r.OwnerOID = 0
			case "missing_database_oid":
				r.DatabaseOID = 0
			}
			before := p.calls
			if _, err := s.ReconcileCheckpointMaintenance(t.Context(), definition, r); err == nil || p.calls != before {
				t.Fatalf("%s reached provider: %v", fault, err)
			}
		})
	}
}

func TestCheckpointMaintenanceServiceRejectsSubstitutedObservations(t *testing.T) {
	s, p, d := maintenanceServiceFixture(t)
	for _, fault := range []string{"token", "source", "owner_oid", "database_oid", "missing_owner", "missing_database", "state"} {
		t.Run(fault, func(t *testing.T) {
			p.fault = fault
			r := CheckpointMaintenanceRequest{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, Phase: "ready", OwnerOID: 10101, DatabaseOID: 20202}
			if actual, err := s.ReconcileCheckpointMaintenance(t.Context(), d, r); !errors.Is(err, ErrConflict) || actual != (CheckpointMaintenance{}) {
				t.Fatalf("accepted %s: %+v %v", fault, actual, err)
			}
		})
	}
}
