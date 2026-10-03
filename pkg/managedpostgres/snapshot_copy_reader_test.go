// adr:375
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type serviceSnapshotReaderProvider struct {
	serviceSnapshotRestoreProvider
	readerCreates, readerFinds int
	readerRequest              SnapshotCopyReaderRequest
	readerFault                string
}

func (p *serviceSnapshotReaderProvider) readerResult(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest) (SnapshotCopyReaderObservation, error) {
	p.definition, p.readerRequest = d, r
	_, p.deadline = ctx.Deadline()
	o := SnapshotCopyReaderObservation{EndpointID: "ep-owned", CreatedAt: r.CaptureCreatedAt.Add(time.Minute), Available: true}
	switch p.readerFault {
	case "source":
		o.EndpointID = d.ProviderResourceID
	case "dataset":
		o.EndpointID = d.DataResourceID
	case "capture":
		o.EndpointID = r.Capture.ExpectedTargetResourceID
	case "pin":
		o.EndpointID = "ep-replacement"
	case "time_pin":
		o.CreatedAt = o.CreatedAt.Add(time.Second)
	case "old":
		o.CreatedAt = r.CaptureCreatedAt.Add(-time.Second)
	case "future":
		o.CreatedAt = time.Now().Add(time.Hour)
	case "submicrosecond":
		o.CreatedAt = o.CreatedAt.Add(time.Nanosecond)
	case "missing":
		return o, ErrNotFound
	}
	return o, nil
}
func (p *serviceSnapshotReaderProvider) PrepareSnapshotCopyReader(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest) (SnapshotCopyReaderObservation, error) {
	p.readerCreates++
	return p.readerResult(ctx, d, r)
}
func (p *serviceSnapshotReaderProvider) FindSnapshotCopyReader(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest) (SnapshotCopyReaderObservation, error) {
	p.readerFinds++
	return p.readerResult(ctx, d, r)
}

func snapshotReaderServiceFixture(t *testing.T) (*Service, *serviceSnapshotReaderProvider, RestoreSourceDefinition, SnapshotCopyReaderRequest) {
	t.Helper()
	_, original, d, capture := snapshotRestoreServiceFixture(t)
	p := &serviceSnapshotReaderProvider{serviceSnapshotRestoreProvider: *original}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	capture.ExpectedTargetResourceID = "project/target"
	r := SnapshotCopyReaderRequest{ResourceID: "reader-owner", Capture: capture, SnapshotCreatedAt: capture.Snapshot.PointInTime.Add(time.Minute),
		CaptureCreatedAt: capture.Snapshot.PointInTime.Add(2 * time.Minute), RequestedAt: capture.Snapshot.PointInTime.Add(3 * time.Minute)}
	return s, p, d, r
}

func TestSnapshotCopyReaderServicePreservesFrozenDefinitionDeadlineAndDiscovery(t *testing.T) {
	s, p, d, r := snapshotReaderServiceFixture(t)
	o, err := s.PrepareSnapshotCopyReader(t.Context(), "account", d, r)
	if err != nil || !o.Available || p.readerRequest != r || p.definition != d || !p.deadline || p.readerCreates != 1 {
		t.Fatalf("reader create: %+v %v", o, err)
	}
	r.ExpectedEndpointID, r.ExpectedCreatedAt = o.EndpointID, o.CreatedAt
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	if _, err := s.PrepareSnapshotCopyReader(t.Context(), "account", d, r); !errors.Is(err, ErrUnavailable) || p.readerCreates != 1 {
		t.Fatalf("disabled create: %v", err)
	}
	if _, err := s.FindSnapshotCopyReader(t.Context(), d, r); err != nil || p.readerFinds != 1 || p.readerCreates != 1 || p.readerRequest != r || !p.deadline {
		t.Fatalf("ungated recovery: %v", err)
	}
	p.readerFault = "missing"
	if _, err := s.FindSnapshotCopyReader(t.Context(), d, r); !errors.Is(err, ErrNotFound) || p.readerCreates != 1 {
		t.Fatalf("absence repeated creation: %v", err)
	}
}

func TestSnapshotCopyReaderServiceRejectsAliasesReplacementAndUnownedDispatch(t *testing.T) {
	for _, mode := range []string{"source", "dataset", "capture", "pin", "time_pin", "old", "future", "submicrosecond"} {
		t.Run(mode, func(t *testing.T) {
			s, p, d, r := snapshotReaderServiceFixture(t)
			r.ExpectedEndpointID = "ep-owned"
			r.ExpectedCreatedAt = r.CaptureCreatedAt.Add(time.Minute)
			p.readerFault = mode
			if _, err := s.FindSnapshotCopyReader(t.Context(), d, r); !errors.Is(err, ErrConflict) {
				t.Fatalf("foreign observation: %v", err)
			}
		})
	}
	for _, mode := range []string{"backend", "fingerprint", "source", "capture", "intent", "future_intent", "endpoint_pin", "time_pin", "account", "admission"} {
		t.Run(mode, func(t *testing.T) {
			s, p, d, r := snapshotReaderServiceFixture(t)
			switch mode {
			case "backend":
				d.BackendID = "missing"
			case "fingerprint":
				d.BackendFingerprint = "changed"
			case "source":
				d.DataResourceID = "project/other"
			case "capture":
				r.Capture.ExpectedTargetResourceID = ""
			case "intent":
				r.RequestedAt = time.Time{}
			case "future_intent":
				r.RequestedAt = time.Now().Add(time.Hour)
			case "endpoint_pin":
				r.ExpectedEndpointID = "ep-owned"
			case "time_pin":
				r.ExpectedCreatedAt = r.CaptureCreatedAt
			case "account":
				s.provisioningAllowed = func(context.Context, string) bool { return false }
			case "admission":
				s.admit = func(context.Context, string) error { return ErrQuotaExceeded }
			}
			if _, err := s.PrepareSnapshotCopyReader(t.Context(), "account", d, r); err == nil || p.readerCreates != 0 {
				t.Fatalf("invalid ownership reached provider: %v", err)
			}
		})
	}
}
