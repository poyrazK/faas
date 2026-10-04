// adr:568
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type serviceConnectionClosureProvider struct {
	serviceMaintenanceProvider
	closes, observes int
	maintenance      CheckpointMaintenance
	request          CheckpointConnectionRequest
	result           CheckpointConnectionClosure
	fault            string
	cancel           context.CancelFunc
}

func (p *serviceConnectionClosureProvider) closureResult(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	p.definition, p.maintenance, p.request = d, m, r
	_, p.deadline = ctx.Deadline()
	names := slices.Clone(r.DatabaseNames)
	slices.Sort(names)
	o := CheckpointConnectionClosure{CheckpointConnectionIdentity: r.CheckpointConnectionIdentity, State: "closed",
		ClosedAt: time.Now().UTC().Truncate(time.Microsecond), Drained: true}
	for i, name := range names {
		o.Databases = append(o.Databases, CheckpointConnectionDatabase{OID: uint32(101 + i), OwnerOID: 201, Name: name, OriginalAllowConnections: i == 0})
	}
	switch p.fault {
	case "owner":
		o.OwnerToken = uuid.NewString()
	case "source":
		o.SourceResourceID += "/other"
	case "state":
		o.State = "released"
	case "missing_time":
		o.ClosedAt = time.Time{}
	case "future_time":
		o.ClosedAt = o.ClosedAt.Add(time.Minute)
	case "precision":
		o.ClosedAt = o.ClosedAt.Add(time.Nanosecond)
	case "missing_database":
		o.Databases = o.Databases[:1]
	case "name":
		o.Databases[0].Name = "other"
	case "missing_oid":
		o.Databases[0].OID = 0
	case "missing_owner":
		o.Databases[0].OwnerOID = 0
	case "duplicate_oid":
		o.Databases[0].OID = o.Databases[1].OID
	case "negative_sessions":
		o.Databases[0].Sessions = -1
	case "false_drain":
		o.Databases[0].Sessions = 1
	case "false_busy":
		o.Drained = false
	case "busy":
		o.Databases[0].Sessions = 1
		o.Drained = false
	case "order":
		slices.Reverse(o.Databases)
	case "mutated_request":
		r.DatabaseNames[0] = "changed-by-provider"
		o.Databases[1].Name = r.DatabaseNames[0]
	case "canceled":
		p.cancel()
	case "error":
		return o, ErrUnavailable
	}
	p.result = o
	return o, nil
}

func (p *serviceConnectionClosureProvider) CloseCheckpointConnections(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	p.closes++
	return p.closureResult(ctx, d, m, r)
}

func (p *serviceConnectionClosureProvider) ObserveCheckpointConnectionClosure(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	p.observes++
	return p.closureResult(ctx, d, m, r)
}

func connectionClosureServiceFixture(t *testing.T) (*Service, *serviceConnectionClosureProvider, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionRequest) {
	t.Helper()
	p := &serviceConnectionClosureProvider{serviceMaintenanceProvider: serviceMaintenanceProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}}}
	registry := testRegistry(t, p, nil)
	s := testService(t, registry, NewMemoryStore())
	b, err := registry.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	d := RestoreSourceDefinition{Spec: testSpec(), BackendID: b.ID, BackendFingerprint: b.Fingerprint, ProviderResourceID: "project", DataResourceID: "project/branch"}
	m := CheckpointMaintenance{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID, State: "ready", OwnerOID: 10001, DatabaseOID: 20002}
	r := CheckpointConnectionRequest{CheckpointConnectionIdentity: CheckpointConnectionIdentity{OwnerToken: uuid.NewString(), SourceResourceID: d.DataResourceID}, DatabaseNames: []string{"z\";%", "a"}}
	return s, p, d, m, r
}

func TestCheckpointConnectionClosureServicePreservesScopeAndSeparatesDrain(t *testing.T) {
	s, p, d, m, r := connectionClosureServiceFixture(t)
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	for _, call := range []func(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionRequest) (CheckpointConnectionClosure, error){s.CloseCheckpointConnections, s.ObserveCheckpointConnectionClosure} {
		for _, fault := range []string{"", "busy"} {
			p.fault = fault
			actual, err := call(t.Context(), d, m, r)
			if err != nil || actual.Validate(r) != nil || actual.Drained != (fault == "") || p.definition != d || p.maintenance != m ||
				!reflect.DeepEqual(p.request, r) || !p.deadline || !slices.Equal(r.DatabaseNames, []string{"z\";%", "a"}) {
				t.Fatalf("%s scoped closure: %+v %v", fault, actual, err)
			}
			p.result.Databases[0].Name = "changed-after-reply"
			if actual.Databases[0].Name != "a" {
				t.Fatal("returned evidence aliases provider result")
			}
		}
	}
	if p.closes != 2 || p.observes != 2 {
		t.Fatalf("close/observe dispatches: %d/%d", p.closes, p.observes)
	}
}

func TestCheckpointConnectionClosureServiceRejectsAuthorityBeforeIO(t *testing.T) {
	s, p, d, m, r := connectionClosureServiceFixture(t)
	for _, call := range []func(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionRequest) (CheckpointConnectionClosure, error){s.CloseCheckpointConnections, s.ObserveCheckpointConnectionClosure} {
		for _, fault := range []string{"owner", "same_owner", "source", "maintenance_source", "maintenance_state", "missing_oid", "backend", "fingerprint", "region", "empty", "duplicate", "nul", "invalid_utf8", "oversize_name", "oversize_set"} {
			definition, maintenance, request := d, m, r
			request.DatabaseNames = slices.Clone(r.DatabaseNames)
			switch fault {
			case "owner":
				request.OwnerToken = uuid.Nil.String()
			case "same_owner":
				request.OwnerToken = maintenance.OwnerToken
			case "source":
				request.SourceResourceID += "/other"
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
				definition.Spec.Region = "other"
			case "empty":
				request.DatabaseNames = nil
			case "duplicate":
				request.DatabaseNames[0] = request.DatabaseNames[1]
			case "nul":
				request.DatabaseNames[0] = "db\x00"
			case "invalid_utf8":
				request.DatabaseNames[0] = "db\xff"
			case "oversize_name":
				request.DatabaseNames[0] = strings.Repeat("ü", 32)
			case "oversize_set":
				request.DatabaseNames = make([]string, api.PostgresCheckpointDatabasesMax+1)
			}
			before := p.closes + p.observes
			actual, err := call(t.Context(), definition, maintenance, request)
			if err == nil || p.closes+p.observes != before || !reflect.DeepEqual(actual, CheckpointConnectionClosure{}) {
				t.Fatalf("%s authority reached provider: %+v %v", fault, actual, err)
			}
		}
	}
	request := r
	request.DatabaseNames = make([]string, api.PostgresCheckpointDatabasesMax)
	for i := range request.DatabaseNames {
		request.DatabaseNames[i] = fmt.Sprintf("database_%04d", i)
	}
	if actual, err := s.CloseCheckpointConnections(t.Context(), d, m, request); err != nil || len(actual.Databases) != api.PostgresCheckpointDatabasesMax {
		t.Fatalf("exact structural limit: %+v %v", actual, err)
	}
	var missing *Service
	if actual, err := missing.CloseCheckpointConnections(t.Context(), d, m, r); !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(actual, CheckpointConnectionClosure{}) {
		t.Fatalf("nil service: %+v %v", actual, err)
	}
	maintenanceOnly, _, frozen := maintenanceServiceFixture(t)
	m.SourceResourceID, r.SourceResourceID = frozen.DataResourceID, frozen.DataResourceID
	if actual, err := maintenanceOnly.CloseCheckpointConnections(t.Context(), frozen, m, r); !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(actual, CheckpointConnectionClosure{}) {
		t.Fatalf("missing optional capability: %+v %v", actual, err)
	}
}

func TestCheckpointConnectionClosureServiceRejectsFalseOrCanceledEvidence(t *testing.T) {
	s, p, d, m, r := connectionClosureServiceFixture(t)
	for _, call := range []func(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionRequest) (CheckpointConnectionClosure, error){s.CloseCheckpointConnections, s.ObserveCheckpointConnectionClosure} {
		for _, fault := range []string{"owner", "source", "state", "missing_time", "future_time", "precision", "missing_database", "name", "missing_oid", "missing_owner", "duplicate_oid", "negative_sessions", "false_drain", "false_busy", "order", "mutated_request", "error", "canceled"} {
			ctx, cancel := context.WithCancel(t.Context())
			p.fault, p.cancel = fault, cancel
			want := ErrConflict
			if fault == "error" {
				want = ErrUnavailable
			}
			if fault == "canceled" {
				want = context.Canceled
			}
			actual, err := call(ctx, d, m, r)
			cancel()
			if !errors.Is(err, want) || !reflect.DeepEqual(actual, CheckpointConnectionClosure{}) || !slices.Equal(r.DatabaseNames, []string{"z\";%", "a"}) {
				t.Fatalf("accepted %s evidence: %+v %v", fault, actual, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := p.closes + p.observes
	if actual, err := s.CloseCheckpointConnections(ctx, d, m, r); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(actual, CheckpointConnectionClosure{}) || p.closes+p.observes != before {
		t.Fatalf("canceled request reached provider: %+v %v", actual, err)
	}
}

func TestCheckpointConnectionSelectionAndObservationKeepPrivateMetadata(t *testing.T) {
	s, _, d, m, r := connectionClosureServiceFixture(t)
	r.DatabaseNames = []string{"private_database_alpha", "private_database_beta"}
	o, err := s.CloseCheckpointConnections(t.Context(), d, m, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{r, o} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, rendered := range []string{string(encoded), fmt.Sprint(value), fmt.Sprintf("%#v", value), fmt.Sprintf("%+v", value)} {
			for _, private := range append(slices.Clone(r.DatabaseNames), r.OwnerToken, r.SourceResourceID) {
				if strings.Contains(rendered, private) {
					t.Fatal("ordinary serialization exposed private selection/observation")
				}
			}
		}
	}
}
