package sched

// adr: 20261017 vmmd restart adoption

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func noticeFixture(t *testing.T) (*state.MemStore, state.ComputeNode, *RestartNotices) {
	t.Helper()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(context.Background(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	return store, node, NewRestartNotices(store)
}

func TestRestartNoticesFollowTheStore(t *testing.T) {
	store, node, notices := noticeFixture(t)
	ctx := context.Background()
	clock := time.Now()
	notices.now = func() time.Time { return clock }
	if notices.Active(ctx, node.ID) {
		t.Fatal("no notice set, yet active")
	}
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, node.ID, 60); err != nil {
		t.Fatal(err)
	}
	if notices.Active(ctx, node.ID) {
		t.Fatal("the cache refreshed before restartNoticeRefresh passed")
	}
	clock = clock.Add(restartNoticeRefresh)
	if !notices.Active(ctx, node.ID) {
		t.Fatal("notice not seen after a refresh")
	}
	clock = clock.Add(61 * time.Second)
	if notices.Active(ctx, node.ID) {
		t.Fatal("expired notice still active")
	}
	var none *RestartNotices
	if none.Active(ctx, node.ID) {
		t.Fatal("a nil cache reports a notice")
	}
}

// A call that fails just after a notice was set re-reads a cache that is
// older than restartNoticeConfirmAge, though not yet due for a refresh.
func TestRestartNoticeConfirmReReadsARecentCache(t *testing.T) {
	store, node, notices := noticeFixture(t)
	ctx := context.Background()
	clock := time.Now()
	notices.now = func() time.Time { return clock }
	if notices.Active(ctx, node.ID) {
		t.Fatal("no notice set, yet active")
	}
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, node.ID, 60); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(restartNoticeConfirmAge)
	if notices.Active(ctx, node.ID) {
		t.Fatal("Active refreshed early")
	}
	if !notices.confirm(ctx, node.ID) {
		t.Fatal("confirm did not see the new notice")
	}
}

func TestCreatesRetryThroughARestartNotice(t *testing.T) {
	store, node, notices := noticeFixture(t)
	ctx := context.Background()
	unavailable := status.Error(codes.Unavailable, "connection refused")
	calls := 0
	flaky := func(failures int, err error) func() (int, error) {
		return func() (int, error) {
			calls++
			if calls <= failures {
				return 0, err
			}
			return 7, nil
		}
	}

	// Without a notice, an unavailable vmmd fails the create at once.
	calls = 0
	if _, err := withRestartNoticeRetry(ctx, notices, node.ID, flaky(1, unavailable)); err == nil || calls != 1 {
		t.Fatalf("no notice: %v after %d calls", err, calls)
	}
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, node.ID, api.VMMDRestartNoticeSeconds); err != nil {
		t.Fatal(err)
	}
	notices.until = nil // Force a refresh.
	calls = 0
	if v, err := withRestartNoticeRetry(ctx, notices, node.ID, flaky(2, fmt.Errorf("wake: %w", unavailable))); err != nil || v != 7 || calls != 3 {
		t.Fatalf("under a notice: %d, %v after %d calls; want the third to succeed", v, err, calls)
	}
	// Other failures are not retried.
	calls = 0
	if _, err := withRestartNoticeRetry(ctx, notices, node.ID, flaky(1, errors.New("snapshot missing"))); err == nil || calls != 1 {
		t.Fatalf("non-transport failure retried: %v after %d calls", err, calls)
	}
	// The wake's own deadline bounds the wait.
	short, cancel := context.WithTimeout(ctx, 2*restartNoticeRetryEvery)
	defer cancel()
	calls = 0
	if _, err := withRestartNoticeRetry(short, notices, node.ID, flaky(1000, unavailable)); err == nil || calls > 4 {
		t.Fatalf("deadline not honoured: %v after %d calls", err, calls)
	}
}

// jobExitAcrossRestart fails its first waits as a stopping vmmd does.
type jobExitAcrossRestart struct {
	*jobVMMWithoutHeldRelease
	failures, calls int
}

func (v *jobExitAcrossRestart) WaitJobExit(context.Context, JobExitSpec) (JobExitResult, error) {
	v.calls++
	if v.calls <= v.failures {
		return JobExitResult{}, status.Error(codes.Unavailable, "vmmd restarting")
	}
	return JobExitResult{ErrorClass: "succeeded", LeaseToken: "lease-1"}, nil
}

// A job-exit wait cut short by a planned vmmd restart is made again against
// the successor; without a notice it fails as before.
func TestJobExitWaitRetriesThroughARestartNotice(t *testing.T) {
	store, node, notices := noticeFixture(t)
	ctx := context.Background()
	client := &jobExitAcrossRestart{jobVMMWithoutHeldRelease: &jobVMMWithoutHeldRelease{fakeRouterVMM: &fakeRouterVMM{}}, failures: 1}
	router := NewVMMRouter([]ComputeNodeInfo{{ID: node.ID, TargetURL: "unix:///node.sock"}},
		func(context.Context, string, *tls.Config) (VMM, error) { return client, nil }, nil).WithRestartNotices(notices)
	spec := JobExitSpec{NodeID: node.ID, InstanceID: "job-1"}
	if _, err := router.WaitJobExit(ctx, spec); status.Code(err) != codes.Unavailable || client.calls != 1 {
		t.Fatalf("no notice: %v after %d calls", err, client.calls)
	}
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, node.ID, api.VMMDRestartNoticeSeconds); err != nil {
		t.Fatal(err)
	}
	notices.until = nil
	client.calls = 0
	got, err := router.WaitJobExit(ctx, spec)
	if err != nil || got.ErrorClass != "succeeded" || client.calls != 2 {
		t.Fatalf("under a notice: %+v, %v after %d calls", got, err, client.calls)
	}
}

// destroyAcrossRestart fails its first destroys as a stopping vmmd does.
type destroyAcrossRestart struct {
	*fakeRouterVMM
	failures, calls int
}

func (v *destroyAcrossRestart) Destroy(context.Context, string) error {
	v.calls++
	if v.calls <= v.failures {
		return status.Error(codes.Unavailable, "vmmd restarting")
	}
	return nil
}

// A destroy, which an app task's or execution's teardown acknowledgement
// waits on, is made again against the successor under a notice.
func TestDestroyRetriesThroughARestartNotice(t *testing.T) {
	store, node, notices := noticeFixture(t)
	ctx := context.Background()
	client := &destroyAcrossRestart{fakeRouterVMM: &fakeRouterVMM{}, failures: 2}
	router := NewVMMRouter([]ComputeNodeInfo{{ID: node.ID, TargetURL: "unix:///node.sock"}},
		func(context.Context, string, *tls.Config) (VMM, error) { return client, nil }, nil).WithRestartNotices(notices)
	if err := router.Destroy(ctx, node.ID, "task-vm"); status.Code(err) != codes.Unavailable || client.calls != 1 {
		t.Fatalf("no notice: %v after %d calls", err, client.calls)
	}
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, node.ID, api.VMMDRestartNoticeSeconds); err != nil {
		t.Fatal(err)
	}
	notices.until = nil
	client.calls = 0
	if err := router.Destroy(ctx, node.ID, "task-vm"); err != nil || client.calls != 3 {
		t.Fatalf("under a notice: %v after %d calls", err, client.calls)
	}
}

func TestPlacementTriesNodesWithoutANoticeFirst(t *testing.T) {
	store, local, notices := noticeFixture(t)
	ctx := context.Background()
	other, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "other", TargetURL: "unix:///tmp/other", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	nodes := []state.ComputeNode{local, other}
	if got, ok := preferUnnoticed(ctx, notices, nodes); ok || len(got) != 2 {
		t.Fatalf("no notice: %d nodes, preferred=%v", len(got), ok)
	}
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, local.ID, 60); err != nil {
		t.Fatal(err)
	}
	notices.until = nil
	if got, ok := preferUnnoticed(ctx, notices, nodes); !ok || len(got) != 1 || got[0].ID != other.ID {
		t.Fatalf("one restarting node: %+v, preferred=%v", got, ok)
	}
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, other.ID, 60); err != nil {
		t.Fatal(err)
	}
	notices.until = nil
	if got, ok := preferUnnoticed(ctx, notices, nodes); ok || len(got) != 2 {
		t.Fatalf("every node restarting: %d nodes, preferred=%v; want all of them", len(got), ok)
	}
}

func TestHeartbeatIgnoresFailedPingsUnderARestartNotice(t *testing.T) {
	store, node, notices := noticeFixture(t)
	ctx := context.Background()
	if err := store.SetComputeNodeVMMDRestartNotice(ctx, node.ID, 60); err != nil {
		t.Fatal(err)
	}
	dialer := &heartbeatFakeDialer{pingErr: map[string]error{node.TargetURL: errors.New("connection refused")}}
	h := NewHeartbeat(store, dialer, nil, nil).WithRestartNotices(notices)
	for i := 0; i < 2*DefaultHeartbeatFailureThreshold; i++ {
		if err := h.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.ComputeNodeByID(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Active {
		t.Fatal("a restarting node was marked unavailable")
	}
	// Once the notice ends the ordinary threshold applies.
	if err := store.ClearComputeNodeVMMDRestartNotice(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	notices.until = nil
	for i := 0; i < DefaultHeartbeatFailureThreshold; i++ {
		if err := h.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if after, _ := store.ComputeNodeByID(ctx, node.ID); after.Active {
		t.Fatal("failed pings after the notice were not counted")
	}
}
