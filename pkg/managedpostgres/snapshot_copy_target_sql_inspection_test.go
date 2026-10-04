// adr:531
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type targetSQLInspectionProvider struct {
	serviceSnapshotCopyProvider
	actual SnapshotCopyTargetSQLObservation
	calls  int
	err    error
}

func (p *targetSQLInspectionProvider) InspectSnapshotCopyTargetSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetSQLObservation, error) {
	p.calls++
	p.definition, p.copyRequest = d, r
	_, p.deadline = ctx.Deadline()
	return p.actual, p.err
}

func targetSQLInspectionServiceFixture(t *testing.T) (*Service, *targetSQLInspectionProvider, RestoreSourceDefinition, SnapshotCopyTargetRequest) {
	t.Helper()
	_, original, d, r := targetSQLServiceFixture(t)
	x := r.Target
	p := &targetSQLInspectionProvider{serviceSnapshotCopyProvider: original.serviceSnapshotCopyProvider, actual: SnapshotCopyTargetSQLObservation{
		ProviderResourceID: x.ProviderResourceID, ProviderCreatedAt: x.ProviderCreatedAt, DataResourceID: x.DataResourceID, EndpointID: x.EndpointID, EndpointCreatedAt: x.EndpointCreatedAt,
		Identity: SnapshotCopyTargetSQLIdentity{PostgresMajor: d.Spec.PostgresMajor, DatabaseName: x.DatabaseName, DatabaseOID: x.DatabaseOID, RoleName: x.RoleName, RoleOID: x.RoleOID}}}
	return testService(t, testRegistry(t, p, nil), NewMemoryStore()), p, d, r.Preparation
}

func TestSnapshotCopyTargetSQLInspectionServiceDiscoversPrivatePinsWithoutNewAdmission(t *testing.T) {
	s, p, d, r := targetSQLInspectionServiceFixture(t)
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	s.admit = func(context.Context, string) error { return ErrQuotaExceeded }
	actual, err := s.InspectSnapshotCopyTargetSQL(t.Context(), d, r)
	if err != nil || actual != p.actual || p.calls != 1 || p.definition != d || p.copyRequest != r || !p.deadline || p.copyCreates+p.copyFinds != 0 {
		t.Fatalf("owned bootstrap inspection: %v", err)
	}
	raw, _ := json.Marshal(actual)
	for _, value := range []string{string(raw), fmt.Sprint(actual), fmt.Sprintf("%#v", actual)} {
		for _, private := range []string{actual.Identity.DatabaseName, actual.Identity.RoleName, actual.ProviderResourceID, actual.DataResourceID, actual.EndpointID} {
			if strings.Contains(value, private) {
				t.Fatal("bootstrap observation exposed private SQL/provider pins")
			}
		}
	}
}

func TestSnapshotCopyTargetSQLInspectionServiceRejectsUnownedRequestsAndSubstitutedObservations(t *testing.T) {
	for _, fault := range []string{"unpinned", "source", "registry", "unsupported", "project", "project_time", "source_data", "capture_data", "data_project", "endpoint", "endpoint_time", "future_endpoint", "precision", "major", "database", "database_nul", "database_long", "role", "database_oid", "role_oid", "provider_error", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := targetSQLInspectionServiceFixture(t)
			ctx := t.Context()
			pre := false
			want := error(ErrConflict)
			switch fault {
			case "unpinned":
				r.ExpectedProviderResourceID, r.ExpectedCreatedAt = "", time.Time{}
				pre = true
				want = ErrInvalid
			case "source":
				r.Capture.Snapshot.SourceResourceID = "source/other"
				pre = true
				want = ErrInvalid
			case "registry":
				d.BackendFingerprint = strings.Repeat("f", 64)
				pre = true
				want = ErrUnavailable
			case "unsupported":
				s = testService(t, testRegistry(t, &p.serviceSnapshotCopyProvider, nil), NewMemoryStore())
				pre = true
				want = ErrUnsupported
			case "project":
				p.actual.ProviderResourceID = "other-project"
			case "project_time":
				p.actual.ProviderCreatedAt = p.actual.ProviderCreatedAt.Add(-time.Second)
			case "source_data":
				p.actual.DataResourceID = d.DataResourceID
			case "capture_data":
				p.actual.DataResourceID = r.Capture.ExpectedTargetResourceID
			case "data_project":
				p.actual.DataResourceID = p.actual.ProviderResourceID
			case "endpoint":
				p.actual.EndpointID = d.DataResourceID
			case "endpoint_time":
				p.actual.EndpointCreatedAt = p.actual.ProviderCreatedAt.Add(-time.Second)
			case "future_endpoint":
				p.actual.EndpointCreatedAt = time.Now().Add(time.Hour)
			case "precision":
				p.actual.EndpointCreatedAt = p.actual.EndpointCreatedAt.Add(time.Nanosecond)
			case "major":
				p.actual.Identity.PostgresMajor++
			case "database":
				p.actual.Identity.DatabaseName = ""
			case "database_nul":
				p.actual.Identity.DatabaseName = "private\x00database"
			case "database_long":
				p.actual.Identity.DatabaseName = strings.Repeat("d", 64)
			case "role":
				p.actual.Identity.RoleName = ""
			case "database_oid":
				p.actual.Identity.DatabaseOID = 0
			case "role_oid":
				p.actual.Identity.RoleOID = 0
			case "provider_error":
				p.err = ErrUnavailable
				want = ErrUnavailable
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			actual, err := s.InspectSnapshotCopyTargetSQL(ctx, d, r)
			if !errors.Is(err, want) || actual != (SnapshotCopyTargetSQLObservation{}) || pre && p.calls != 0 {
				t.Fatalf("unqualified bootstrap pins escaped: %v", err)
			}
		})
	}
}
