// adr: 590
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type serviceConnectionDiscoveryProvider struct {
	serviceMaintenanceProvider
	calls    int
	deadline bool
	result   CheckpointConnectionRequest
	fault    string
	cancel   context.CancelFunc
}

func (p *serviceConnectionDiscoveryProvider) DiscoverCheckpointConnections(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, identity CheckpointConnectionIdentity) (CheckpointConnectionRequest, error) {
	p.calls++
	p.definition = d
	_, p.deadline = ctx.Deadline()
	r := CheckpointConnectionRequest{CheckpointConnectionIdentity: identity, DatabaseNames: []string{"a", "z\";%"}}
	switch p.fault {
	case "owner":
		r.OwnerToken = uuid.NewString()
	case "source":
		r.SourceResourceID += "/other"
	case "empty":
		r.DatabaseNames = nil
	case "duplicate":
		r.DatabaseNames[1] = r.DatabaseNames[0]
	case "order":
		r.DatabaseNames[0], r.DatabaseNames[1] = r.DatabaseNames[1], r.DatabaseNames[0]
	case "nul":
		r.DatabaseNames[0] = "a\x00"
	case "oversize":
		r.DatabaseNames = make([]string, api.PostgresCheckpointDatabasesMax+1)
	case "cancel":
		p.cancel()
	case "error":
		return r, ErrUnavailable
	}
	p.result = r
	return r, nil
}

func TestCheckpointConnectionDiscoveryServiceFencesProviderAndOwnsResult(t *testing.T) {
	p := &serviceConnectionDiscoveryProvider{serviceMaintenanceProvider: serviceMaintenanceProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}}}
	registry := testRegistry(t, p, nil)
	s := testService(t, registry, NewMemoryStore())
	backend, err := registry.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	d := RestoreSourceDefinition{Spec: testSpec(), BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "project", DataResourceID: "project/branch"}
	m := CheckpointMaintenance{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, State: "ready", OwnerOID: 101, DatabaseOID: 202}
	identity := CheckpointConnectionIdentity{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID}
	actual, err := s.DiscoverCheckpointConnections(t.Context(), d, m, identity)
	if err != nil || actual.Validate() != nil || actual.CheckpointConnectionIdentity != identity || !p.deadline || p.definition != d {
		t.Fatalf("discovery authority: %v", err)
	}
	p.result.DatabaseNames[0] = "provider-mutated-private-name"
	if actual.DatabaseNames[0] != "a" {
		t.Fatal("discovery aliases provider output")
	}
	for _, fault := range []string{"owner", "source", "empty", "duplicate", "order", "nul", "oversize", "cancel", "error"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p.fault, p.cancel = fault, cancel
			actual, err := s.DiscoverCheckpointConnections(ctx, d, m, identity)
			if err == nil || !reflect.DeepEqual(actual, CheckpointConnectionRequest{}) {
				t.Fatalf("accepted %s discovery: %v", fault, err)
			}
			if fault == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
	p.fault = ""
	for _, fault := range []string{"identity", "same_owner", "source", "maintenance_state", "maintenance_oid", "backend", "backend_fingerprint", "region"} {
		definition, maintenance, owner := d, m, identity
		switch fault {
		case "identity":
			owner.OwnerToken = uuid.Nil.String()
		case "same_owner":
			owner.OwnerToken = maintenance.OwnerToken
		case "source":
			owner.SourceResourceID += "/other"
		case "maintenance_state":
			maintenance.State = "reserved"
		case "maintenance_oid":
			maintenance.DatabaseOID = 0
		case "backend":
			definition.BackendID = "other"
		case "backend_fingerprint":
			definition.BackendFingerprint = strings.Repeat("b", 64)
		case "region":
			definition.Spec.Region = "other"
		}
		before := p.calls
		actual, err := s.DiscoverCheckpointConnections(t.Context(), definition, maintenance, owner)
		if err == nil || p.calls != before || !reflect.DeepEqual(actual, CheckpointConnectionRequest{}) {
			t.Fatalf("%s reached provider: %v", fault, err)
		}
	}
	missing, _, original := maintenanceServiceFixture(t)
	m.SourceResourceID, identity.SourceResourceID = original.DataResourceID, original.DataResourceID
	if _, err := missing.DiscoverCheckpointConnections(t.Context(), original, m, identity); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("optional capability: %v", err)
	}
	var unavailable *Service
	if _, err := unavailable.DiscoverCheckpointConnections(t.Context(), d, m, CheckpointConnectionIdentity{OwnerToken: identity.OwnerToken, SourceResourceID: d.DataResourceID}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil service: %v", err)
	}
	private := CheckpointConnectionRequest{CheckpointConnectionIdentity: identity, DatabaseNames: []string{"private-catalogue-name"}}
	raw, err := json.Marshal(private)
	if err != nil || strings.Contains(string(raw)+fmt.Sprintf("%+v %#v", private, private), "private-catalogue-name") {
		t.Fatal("discovery leaked database names")
	}
}
