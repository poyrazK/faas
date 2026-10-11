// adr: 590
package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type serviceReaderDeletionProvider struct {
	serviceSnapshotReaderProvider
	readerDeletes, readerDeletionReads int
	readerDeletionRequest              SnapshotCopyReaderDeletionRequest
}

func (p *serviceReaderDeletionProvider) DeleteSnapshotCopyReader(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDeletionRequest) (SnapshotCopyReaderDeletionObservation, error) {
	p.readerDeletes++
	return p.readerDeletionResult(ctx, d, r, false), nil
}
func (p *serviceReaderDeletionProvider) ObserveSnapshotCopyReaderDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDeletionRequest) (SnapshotCopyReaderDeletionObservation, error) {
	p.readerDeletionReads++
	return p.readerDeletionResult(ctx, d, r, true), nil
}
func (p *serviceReaderDeletionProvider) readerDeletionResult(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDeletionRequest, done bool) SnapshotCopyReaderDeletionObservation {
	p.definition, p.readerDeletionRequest = d, r
	_, p.deadline = ctx.Deadline()
	o := SnapshotCopyReaderDeletionObservation{EndpointID: r.Reader.ExpectedEndpointID, CreatedAt: r.Reader.ExpectedCreatedAt,
		OperationIDs: []string{"delete-b", "delete-a"}, CaptureOperationIDs: r.CaptureOperationIDs, Done: done}
	switch p.fault {
	case "endpoint":
		o.EndpointID = "ep-other"
	case "created":
		o.CreatedAt = o.CreatedAt.Add(time.Second)
	case "operations":
		o.OperationIDs = []string{"other"}
	case "capture_operations":
		o.CaptureOperationIDs = []string{"other"}
	case "duplicates":
		o.OperationIDs = []string{"same", "same"}
	case "empty":
		o.OperationIDs, o.CaptureOperationIDs = nil, nil
	case "oversize":
		o.OperationIDs = make([]string, api.PostgresCopyReaderMaxOperations+1)
	}
	return o
}

func readerDeletionServiceFixture(t *testing.T) (*Service, *serviceReaderDeletionProvider, RestoreSourceDefinition, SnapshotCopyReaderDeletionRequest) {
	t.Helper()
	_, original, d, reader := snapshotReaderServiceFixture(t)
	p := &serviceReaderDeletionProvider{serviceSnapshotReaderProvider: *original}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	reader.ExpectedEndpointID = "ep-owned"
	reader.ExpectedCreatedAt = reader.CaptureCreatedAt.Add(5 * time.Minute)
	return s, p, d, SnapshotCopyReaderDeletionRequest{Reader: reader, RequestedAt: reader.ExpectedCreatedAt.Add(time.Minute)}
}

func TestSnapshotCopyReaderDeletionServiceKeepsOwnershipAndRecoveryUngated(t *testing.T) {
	s, p, d, r := readerDeletionServiceFixture(t)
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	s.admit = func(context.Context, string) error { return ErrQuotaExceeded }
	o, err := s.DeleteSnapshotCopyReader(t.Context(), d, r)
	if err != nil || o.Done || p.readerDeletes != 1 || !p.deadline || p.definition != d || p.readerDeletionRequest.Reader != r.Reader ||
		!slices.Equal(o.OperationIDs, []string{"delete-a", "delete-b"}) {
		t.Fatalf("private cleanup: %+v %v", o, err)
	}
	r.OperationIDs, r.CaptureOperationIDs = o.OperationIDs, []string{"capture-a"}
	o, err = s.ObserveSnapshotCopyReaderDeletion(t.Context(), d, r)
	if err != nil || !o.Done || p.readerDeletes != 1 || p.readerDeletionReads != 1 || !slices.Equal(o.CaptureOperationIDs, r.CaptureOperationIDs) {
		t.Fatalf("recovery changed ownership: %+v %v", o, err)
	}
	for _, fault := range []string{"endpoint", "created", "operations", "capture_operations", "duplicates", "empty", "oversize"} {
		p.fault = fault
		if _, err := s.ObserveSnapshotCopyReaderDeletion(t.Context(), d, r); !errors.Is(err, ErrConflict) {
			t.Fatalf("accepted %s proof: %v", fault, err)
		}
	}
}

func TestSnapshotCopyReaderDeletionServiceRejectsUnpinnedIntentBeforeIO(t *testing.T) {
	for _, fault := range []string{"endpoint", "time_pin", "cleanup_intent", "old_intent", "future_intent", "precision", "source", "backend", "duplicates", "oversize"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := readerDeletionServiceFixture(t)
			switch fault {
			case "endpoint":
				r.Reader.ExpectedEndpointID = ""
			case "time_pin":
				r.Reader.ExpectedCreatedAt = time.Time{}
			case "cleanup_intent":
				r.RequestedAt = time.Time{}
			case "old_intent":
				r.RequestedAt = r.Reader.ExpectedCreatedAt.Add(-time.Second)
			case "future_intent":
				r.RequestedAt = time.Now().Add(time.Hour)
			case "precision":
				r.RequestedAt = r.RequestedAt.Add(time.Nanosecond)
			case "source":
				d.DataResourceID = "project/other"
			case "backend":
				d.BackendFingerprint = "changed"
			case "duplicates":
				r.OperationIDs = []string{"same", "same"}
			case "oversize":
				r.CaptureOperationIDs = make([]string, api.PostgresCopyReaderMaxOperations+1)
			}
			if _, err := s.DeleteSnapshotCopyReader(t.Context(), d, r); err == nil || p.readerDeletes != 0 {
				t.Fatalf("invalid cleanup reached provider: %v", err)
			}
		})
	}
}
