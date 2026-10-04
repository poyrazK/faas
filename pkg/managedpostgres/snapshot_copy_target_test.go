// adr:531
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type serviceSnapshotCopyProvider struct {
	serviceSnapshotRestoreProvider
	copyCreates, copyFinds int
	copyRequest            SnapshotCopyTargetRequest
	copyFault              string
}

func (p *serviceSnapshotCopyProvider) copyResult(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetObservation, error) {
	p.definition, p.copyRequest = d, r
	_, p.deadline = ctx.Deadline()
	o := SnapshotCopyTargetObservation{ProviderResourceID: "independent-project", CreatedAt: r.CaptureCreatedAt.Add(time.Minute), Spec: d.Spec, Prepared: true}
	switch p.copyFault {
	case "source":
		o.ProviderResourceID = d.ProviderResourceID
	case "capture":
		o.ProviderResourceID = r.Capture.ExpectedTargetResourceID
	case "pin":
		o.ProviderResourceID = "another-project"
	case "spec":
		o.Spec.RestoreWindowSeconds++
	case "created":
		o.CreatedAt = r.CaptureCreatedAt.Add(-time.Second)
	case "future":
		o.CreatedAt = time.Now().Add(time.Hour)
	case "submicrosecond":
		o.CreatedAt = o.CreatedAt.Add(time.Nanosecond)
	case "time_pin":
		o.CreatedAt = o.CreatedAt.Add(time.Second)
	case "missing":
		return SnapshotCopyTargetObservation{}, ErrNotFound
	}
	return o, nil
}

func (p *serviceSnapshotCopyProvider) PrepareSnapshotCopyTarget(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetObservation, error) {
	p.copyCreates++
	return p.copyResult(ctx, d, r)
}
func (p *serviceSnapshotCopyProvider) FindSnapshotCopyTarget(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetObservation, error) {
	p.copyFinds++
	return p.copyResult(ctx, d, r)
}

func snapshotCopyServiceFixture(t *testing.T) (*Service, *serviceSnapshotCopyProvider, RestoreSourceDefinition, SnapshotCopyTargetRequest) {
	t.Helper()
	_, original, d, capture := snapshotRestoreServiceFixture(t)
	p := &serviceSnapshotCopyProvider{serviceSnapshotRestoreProvider: *original}
	registry := testRegistry(t, p, nil)
	s := testService(t, registry, NewMemoryStore())
	capture.ExpectedTargetResourceID = "project/target"
	r := SnapshotCopyTargetRequest{ResourceID: "independent-owner", Capture: capture,
		SnapshotCreatedAt: capture.Snapshot.PointInTime.Add(time.Minute), CaptureCreatedAt: capture.Snapshot.PointInTime.Add(2 * time.Minute)}
	return s, p, d, r
}

func TestSnapshotCopyTargetServicePreservesFrozenInputAndRecovery(t *testing.T) {
	s, p, d, r := snapshotCopyServiceFixture(t)
	o, err := s.PrepareSnapshotCopyTarget(t.Context(), "account", d, r)
	if err != nil || !o.Prepared || p.copyCreates != 1 || p.copyRequest != r || p.definition != d || !p.deadline {
		t.Fatalf("prepare independent project: %+v %v", o, err)
	}
	s.provisioningEnabled = func() bool { return false }
	if _, err := s.PrepareSnapshotCopyTarget(t.Context(), "account", d, r); !errors.Is(err, ErrUnavailable) || p.copyCreates != 1 {
		t.Fatalf("disabled creation gate: %v", err)
	}
	r.ExpectedProviderResourceID, r.ExpectedCreatedAt = o.ProviderResourceID, o.CreatedAt
	if _, err := s.FindSnapshotCopyTarget(t.Context(), d, r); err != nil || p.copyCreates != 1 || p.copyFinds != 1 || p.copyRequest != r {
		t.Fatalf("disabled discovery lost frozen selectors: %v", err)
	}
}

func TestSnapshotCopyTargetAdmissionUsesIndependentQuotaAfterPITRExpiry(t *testing.T) {
	s, p, d, _ := snapshotCopyServiceFixture(t)
	d.Spec.RestoreWindowSeconds = 0
	s.maxDatabasesPerAccount = func(context.Context, string) (int, error) { return 7, nil }
	limit, err := s.AdmitSnapshotCopyTargetReservation(t.Context(), "account", d)
	if err != nil || limit != 7 || p.copyCreates != 0 || p.copyFinds != 0 {
		t.Fatalf("independent admission depends on PITR: %d %v", limit, err)
	}
	s.admit = func(context.Context, string) error { return ErrQuotaExceeded }
	if _, err := s.AdmitSnapshotCopyTargetReservation(t.Context(), "account", d); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("independent project bypassed account admission: %v", err)
	}
}

func TestSnapshotCopyTargetServiceRejectsInvalidAuthorityBeforeIO(t *testing.T) {
	for _, fault := range []string{"owner", "shared_owner", "capture_pin", "source", "source_pin", "snapshot_time", "capture_time", "missing_pin_time", "fingerprint", "region"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := snapshotCopyServiceFixture(t)
			switch fault {
			case "owner":
				r.ResourceID = ""
			case "shared_owner":
				r.ResourceID = r.Capture.ResourceID
			case "capture_pin":
				r.Capture.ExpectedTargetResourceID = ""
			case "source":
				d.DataResourceID = "project/other"
			case "source_pin":
				r.ExpectedProviderResourceID = d.ProviderResourceID
				r.ExpectedCreatedAt = r.CaptureCreatedAt.Add(time.Minute)
			case "snapshot_time":
				r.SnapshotCreatedAt = time.Time{}
			case "capture_time":
				r.CaptureCreatedAt = time.Now().Add(time.Hour)
			case "missing_pin_time":
				r.ExpectedProviderResourceID = "independent-project"
			case "fingerprint":
				d.BackendFingerprint = "different"
			case "region":
				d.Spec.Region = "other-region"
			}
			if _, err := s.FindSnapshotCopyTarget(t.Context(), d, r); err == nil || p.copyFinds != 0 {
				t.Fatalf("invalid %s reached provider: %v", fault, err)
			}
		})
	}
}

func TestSnapshotCopyTargetServiceRejectsChangedProjectObservation(t *testing.T) {
	for _, fault := range []string{"source", "capture", "pin", "spec", "created", "future", "submicrosecond", "time_pin", "missing"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := snapshotCopyServiceFixture(t)
			r.ExpectedProviderResourceID = "independent-project"
			r.ExpectedCreatedAt = r.CaptureCreatedAt.Add(time.Minute)
			p.copyFault = fault
			want := ErrConflict
			if fault == "missing" {
				want = ErrNotFound
			}
			if o, err := s.FindSnapshotCopyTarget(t.Context(), d, r); !errors.Is(err, want) || o != (SnapshotCopyTargetObservation{}) || p.copyCreates != 0 {
				t.Fatalf("accepted %s: %+v %v", fault, o, err)
			}
		})
	}
}
