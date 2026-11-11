package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type rejectingResizeProvider struct {
	*resizeTestProvider
	updateErr error
	observe   func(UpdateRequest) (ObservedDatabase, error)
	checks    int
}

func (p *rejectingResizeProvider) Update(_ context.Context, request UpdateRequest) (ObservedDatabase, error) {
	p.calls++
	if p.updateErr != nil {
		return ObservedDatabase{}, p.updateErr
	}
	return ObservedDatabase{ProviderResourceID: request.ResourceID, DataResourceID: request.DataResourceID, Spec: request.Spec, Status: ProviderStatusReady}, nil
}

func (p *rejectingResizeProvider) ObserveUpdate(_ context.Context, request UpdateRequest) (ObservedDatabase, error) {
	p.checks++
	return p.observe(request)
}

func rejectedResizeFixture(t *testing.T) (*Service, *MemoryStore, *rejectingResizeProvider, Database, *time.Time) {
	t.Helper()
	caps := testCapabilities()
	caps.ServiceClasses = append(caps.ServiceClasses, ClassProduction)
	caps.ClassResize, caps.StorageResize, caps.ScaleToZeroUpdate = true, true, true
	p := &rejectingResizeProvider{resizeTestProvider: &resizeTestProvider{fakeProvider: &fakeProvider{
		capabilities: caps, provisionStatus: ProviderStatusReady, deleteDone: true}}, updateErr: ErrInvalid}
	p.observe = func(request UpdateRequest) (ObservedDatabase, error) {
		return ObservedDatabase{ProviderResourceID: request.ResourceID, DataResourceID: request.DataResourceID, Spec: request.PreviousSpec, Status: ProviderStatusReady}, nil
	}
	store := NewMemoryStore()
	now := time.Now().UTC()
	svc, err := NewService(testRegistry(t, p, nil), store, ServiceOptions{Now: func() time.Time { return now }, ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	database, err := svc.Create(t.Context(), CreateRequest{AccountID: "account", Name: "orders", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	return svc, store, p, database, &now
}

func TestResizeRejectionRequiresSettledPinnedObservation(t *testing.T) {
	for _, kind := range []string{"class", "storage", "policy"} {
		for _, outcome := range []string{"source", "target", "pending", "read failure", "resource drift", "spec drift", "expired lease", "transient"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				svc, store, provider, database, clock := rejectedResizeFixture(t)
				base := provider.observe
				provider.observe = func(request UpdateRequest) (ObservedDatabase, error) {
					observed, _ := base(request)
					switch outcome {
					case "target":
						observed.Spec = request.Spec
					case "pending":
						observed.Status = ProviderStatusPending
					case "read failure":
						return ObservedDatabase{}, ErrUnavailable
					case "resource drift":
						observed.DataResourceID = "other-dataset"
					case "spec drift":
						observed.Spec.PostgresMajor++
					case "expired lease":
						*clock = clock.Add(svc.leaseDuration + time.Second)
					}
					return observed, nil
				}
				if outcome == "transient" {
					provider.updateErr = ErrUnavailable
				}
				request := resizeRequest(database)
				if kind == "storage" {
					request.TargetClass, request.TargetStorageLimitBytes = "", 2*database.Spec.StorageLimitBytes
				}
				var operation ResizeOperation
				var err error
				if kind == "policy" {
					operation, err = svc.ChangeComputePolicy(t.Context(), ChangeComputePolicyRequest{AccountID: database.AccountID, DatabaseID: database.ID, RequestID: request.RequestID, ScaleToZero: false})
				} else {
					operation, err = svc.Resize(t.Context(), request)
				}
				if err != nil {
					t.Fatal(err)
				}
				_, reconcileErr := svc.Reconcile(t.Context(), database.AccountID, database.ID)
				receipt, err := store.GetResize(t.Context(), database.AccountID, operation.ID)
				if err != nil {
					t.Fatal(err)
				}
				current, err := svc.Get(t.Context(), database.AccountID, database.ID)
				if err != nil {
					t.Fatal(err)
				}
				if outcome != "source" && outcome != "target" {
					if reconcileErr == nil || receipt.State != ResizePending || current.State != StateUpdating || current.ObservedGeneration != 1 {
						t.Fatalf("uncertain rejection released its pins: receipt=%+v database=%+v err=%v", receipt, current, reconcileErr)
					}
					if outcome == "transient" && provider.checks != 0 {
						t.Fatal("transient error was treated as a permanent rejection")
					}
					return
				}
				want, spec := ResizeFailed, database.Spec
				if outcome == "target" {
					want, spec = ResizeSucceeded, operation.TargetSpec()
				}
				if reconcileErr != nil || receipt.State != want || current.State != StateReady || current.Spec != spec || current.ObservedGeneration != current.DesiredGeneration || receipt.CompletedAt.IsZero() {
					t.Fatalf("rejection not settled from its observation: receipt=%+v database=%+v err=%v", receipt, current, reconcileErr)
				}
				if want == ResizeFailed && (receipt.ErrorCode != "invalid" || receipt.LastErrorCode != "invalid") {
					t.Fatalf("failed receipt lost its safe diagnostic: %+v", receipt)
				}
			})
		}
	}
}

func TestRejectedResizeAllowsCorrectionAndDeletion(t *testing.T) {
	svc, _, provider, database, _ := rejectedResizeFixture(t)
	request := resizeRequest(database)
	if _, err := svc.Resize(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reconcile(t.Context(), database.AccountID, database.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := svc.Resize(t.Context(), request)
	if err != nil || replay.State != ResizeFailed || provider.calls != 1 {
		t.Fatalf("failed request was resubmitted: %+v calls=%d err=%v", replay, provider.calls, err)
	}
	provider.updateErr = nil
	request.RequestID = uuid.NewString()
	request.TargetClass = ClassProduction
	if _, err := svc.Resize(t.Context(), request); err != nil {
		t.Fatal("corrected resize remained blocked", err)
	}
	if _, err := svc.Reconcile(t.Context(), database.AccountID, database.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Delete(t.Context(), database.AccountID, database.ID); err != nil {
		t.Fatal("purge remained blocked", err)
	}
}

func TestRejectedResizeWithoutConfirmationKeepsPins(t *testing.T) {
	svc, store, provider, database, _ := resizeFixture(t)
	provider.reject = "resize_refused"
	request := resizeRequest(database)
	if _, err := svc.Resize(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reconcile(t.Context(), database.AccountID, database.ID); err == nil {
		t.Fatal("unconfirmed rejection was settled")
	}
	if _, err := store.ClaimDelete(t.Context(), database.AccountID, database.ID, "delete", time.Now(), time.Now().Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatal("unconfirmed provider work allowed a delete", err)
	}
}

func TestRejectedResizePreservesSafeProviderReason(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"unsupported", ErrUnsupported, "unsupported"},
		{"typed rejection", &ChangeRejectedError{Code: "resize_refused"}, "resize_refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, provider, database, _ := rejectedResizeFixture(t)
			provider.updateErr = tc.err
			operation, err := svc.Resize(t.Context(), resizeRequest(database))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Reconcile(t.Context(), database.AccountID, database.ID); err != nil {
				t.Fatal(err)
			}
			receipt, err := store.GetResize(t.Context(), database.AccountID, operation.ID)
			if err != nil || receipt.State != ResizeFailed || receipt.ErrorCode != tc.code {
				t.Fatalf("confirmed rejection lost its reason: %+v err=%v", receipt, err)
			}
		})
	}
}
