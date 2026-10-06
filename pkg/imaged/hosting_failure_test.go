// adr: 434 — interrupted consumers and persistence outages retain replayable candidates.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type hostingFailureFaultStore struct {
	*state.MemStore
	failureErr, receiptErr               error
	failureCalls, receiptCalls, oldCalls int
}

func (s *hostingFailureFaultStore) FailDeploymentWithHostingReceipt(ctx context.Context, id string, raw []byte, code, message string) (bool, error) {
	s.failureCalls++
	if s.failureErr != nil {
		err := s.failureErr
		s.failureErr = nil
		return false, err
	}
	return s.MemStore.FailDeploymentWithHostingReceipt(ctx, id, raw, code, message)
}

func (s *hostingFailureFaultStore) UpsertDeploymentHostingReceipt(ctx context.Context, id string, raw []byte) (state.Deployment, error) {
	s.receiptCalls++
	if s.receiptErr != nil {
		err := s.receiptErr
		s.receiptErr = nil
		return state.Deployment{}, err
	}
	return s.MemStore.UpsertDeploymentHostingReceipt(ctx, id, raw)
}

func (s *hostingFailureFaultStore) SetDeploymentFailed(ctx context.Context, id, code, message string) (state.Deployment, error) {
	s.oldCalls++
	return s.MemStore.SetDeploymentFailed(ctx, id, code, message)
}

type hostingFailureNotifier struct {
	fakeNotifier
	store     *state.MemStore
	failed    int
	commitErr error
}

func (n *hostingFailureNotifier) Notify(ctx context.Context, channel, payload string) error {
	var event struct {
		DeploymentID string `json:"deployment_id"`
		Status       string `json:"status"`
	}
	if channel == db.NotifyDeploymentChanged {
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
	}
	if event.Status == string(state.DeployFailed) {
		n.failed++
		dep, err := n.store.DeploymentByID(ctx, event.DeploymentID)
		receipt, receiptErr := apihostingreceipt.Decode(dep.APIHostingReceipt)
		if err != nil || receiptErr != nil || dep.Status != state.DeployFailed || receipt.Smoke.Status != apihostingreceipt.SmokeFailed {
			n.commitErr = fmt.Errorf("event preceded verdict commit (status=%s): %w", dep.Status, errors.Join(err, receiptErr, state.ErrInvalidStateTransition))
		}
	}
	return n.fakeNotifier.Notify(ctx, channel, payload)
}

type hostingReplay struct {
	store               *hostingFailureFaultStore
	notif               *hostingFailureNotifier
	handler             *Handler
	previous, candidate state.Deployment
	notification        db.Notification
}

func hostingReplayFixture(t *testing.T) *hostingReplay {
	t.Helper()
	ctx := context.Background()
	f := &hostingReplay{store: &hostingFailureFaultStore{MemStore: state.NewMemStore()}}
	account, err := f.store.CreateAccount(ctx, "hosting-replay@example.test", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "hosting-replay", RAMMB: 256, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	f.previous, err = f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: state.DefaultEnvScope, ImageDigest: "sha256:previous", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	f.candidate, err = f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: state.DefaultEnvScope, ImageDigest: "sha256:candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateDeploymentStatus(ctx, f.candidate.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	f.notif = &hostingFailureNotifier{store: f.store.MemStore}
	f.handler = New(f.store, f.notif, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmokeRequired(true)
	f.notification = db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + f.candidate.ID + `","storage_key":"snap/` + f.candidate.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`}
	return f
}

func (f *hostingReplay) assertReplayable(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	dep, err := f.store.DeploymentByID(ctx, f.candidate.ID)
	live, liveErr := f.store.LiveDeploymentForScope(ctx, f.candidate.AppID, state.DefaultEnvScope)
	if err != nil || dep.Status != state.DeploySnapshotting || len(dep.APIHostingReceipt) != 0 || liveErr != nil || live.ID != f.previous.ID || f.notif.failed != 0 || f.store.oldCalls != 0 {
		t.Fatalf("not replayable: status=%s receipt=%s live=%s events=%d legacy=%d err=%v liveErr=%v", dep.Status, dep.APIHostingReceipt, live.ID, f.notif.failed, f.store.oldCalls, err, liveErr)
	}
}

func TestHostingFailurePersistenceOutageReplaysWithoutPrematureEvents(t *testing.T) {
	f := hostingReplayFixture(t)
	outage := errors.New("database unavailable at commit")
	f.store.failureErr = outage
	smokes := 0
	f.handler.WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
		smokes++
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed, StatusCode: 503, Error: "candidate unavailable"}, nil
	})
	if err := f.handler.HandleNotification(context.Background(), f.notification); !errors.Is(err, outage) {
		t.Fatalf("outage not returned to consumer: %v", err)
	}
	f.assertReplayable(t)
	if err := f.handler.HandleNotification(context.Background(), f.notification); err == nil {
		t.Fatal("committed candidate failure was not reported")
	}
	if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil {
		t.Fatalf("terminal redelivery was not acknowledged: %v", err)
	}
	if smokes != 2 || f.store.failureCalls != 2 || f.store.receiptCalls != 0 || f.store.oldCalls != 0 || f.notif.failed != 1 || f.notif.commitErr != nil {
		t.Fatalf("smokes=%d atomic=%d receipts=%d legacy=%d events=%d commit=%v", smokes, f.store.failureCalls, f.store.receiptCalls, f.store.oldCalls, f.notif.failed, f.notif.commitErr)
	}
}

func TestHostingVerifiedReceiptOutageRemainsReplayable(t *testing.T) {
	f := hostingReplayFixture(t)
	outage := errors.New("database unavailable writing receipt")
	f.store.receiptErr = outage
	f.handler.WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: 200}, nil
	})
	if err := f.handler.HandleNotification(context.Background(), f.notification); !errors.Is(err, outage) {
		t.Fatalf("outage not returned to consumer: %v", err)
	}
	f.assertReplayable(t)
	if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil {
		t.Fatal(err)
	}
	live, err := f.store.LiveDeploymentForScope(context.Background(), f.candidate.AppID, state.DefaultEnvScope)
	if err != nil || live.ID != f.candidate.ID || f.store.failureCalls != 0 || f.notif.failed != 0 {
		t.Fatalf("verified candidate did not promote: live=%s err=%v", live.ID, err)
	}
}

func TestHostingConsumerCancellationDoesNotBecomeCandidateFailure(t *testing.T) {
	f := hostingReplayFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	smokes := 0
	f.handler.WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
		smokes++
		if smokes == 1 {
			cancel()
			return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed}, context.Canceled
		}
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: 200}, nil
	})
	if err := f.handler.HandleNotification(ctx, f.notification); !errors.Is(err, context.Canceled) {
		t.Fatalf("consumer cancellation not returned: %v", err)
	}
	f.assertReplayable(t)
	if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil {
		t.Fatal(err)
	}
	live, _ := f.store.LiveDeploymentForScope(context.Background(), f.candidate.AppID, state.DefaultEnvScope)
	if live.ID != f.candidate.ID || f.store.failureCalls != 0 || smokes != 2 {
		t.Fatalf("interrupted candidate not replayed: live=%s atomic=%d smokes=%d", live.ID, f.store.failureCalls, smokes)
	}
}

func TestHostingWithdrawnCandidateIgnoresStaleFailure(t *testing.T) {
	f := hostingReplayFixture(t)
	f.handler.WithHostingSmoke(func(ctx context.Context, _ state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
		if err := f.store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployCancelled, "customer cancelled"); err != nil {
			t.Fatal(err)
		}
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed, StatusCode: 503}, nil
	})
	if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil {
		t.Fatal(err)
	}
	dep, _ := f.store.DeploymentByID(context.Background(), f.candidate.ID)
	if dep.Status != state.DeployCancelled || len(dep.APIHostingReceipt) != 0 || f.notif.failed != 0 || f.store.oldCalls != 0 {
		t.Fatalf("stale verifier replaced cancellation: %+v events=%d", dep, f.notif.failed)
	}
}

func TestHostingProbeDeadlineStillFinalizesVerdict(t *testing.T) {
	f := hostingReplayFixture(t)
	f.handler.WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed, ErrorCode: "smoke_request_failed"}, context.DeadlineExceeded
	})
	if err := f.handler.HandleNotification(context.Background(), f.notification); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe deadline not reported: %v", err)
	}
	dep, _ := f.store.DeploymentByID(context.Background(), f.candidate.ID)
	if dep.Status != state.DeployFailed || f.notif.failed != 1 || f.notif.commitErr != nil {
		t.Fatalf("probe deadline did not finalize: status=%s events=%d commit=%v", dep.Status, f.notif.failed, f.notif.commitErr)
	}
}
