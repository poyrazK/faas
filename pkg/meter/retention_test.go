// Retention cron tests (ADR-049 §B.4). Exercises the
// idempotent-DB-exec contract under the BATCHED DELETE shape
// (PR #428 review blocker #4):
//
//   1. First tick on a 14-month-old dataset — DELETE path
//      returns the row count, loops until short-read.
//   2. Second tick on the same dataset — 0 rows (idempotent).
//   3. Context cancel — Loop returns.
//   4. Exec error — propagated, Loop continues on next tick.
//   5. Batch cap hit — returns (rows, ErrRetentionBatchCap).
//   6. SQL substring pins (ctid subquery + LIMIT $1 + 13-month
//      cutoff) so a refactor back to an unbounded DELETE is
//      caught at unit-test time.

package meter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// rowsFunc lets a test script the per-call rowsAffected return
// so the multi-batch loop's exit condition can be exercised.
type rowsFunc func(callIndex int) int64

type recordingExecer struct {
	mu      sync.Mutex
	calls   []retentionCall
	rowsFn  rowsFunc
	errFn   func(callIndex int) error
	callIdx int
}

type retentionCall struct {
	SQL  string
	Args []any
}

func (r *recordingExecer) Exec(_ context.Context, sql string, args ...any) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]any, len(args))
	copy(cp, args)
	r.calls = append(r.calls, retentionCall{SQL: sql, Args: cp})
	idx := r.callIdx
	r.callIdx++
	if r.errFn != nil {
		if err := r.errFn(idx); err != nil {
			return 0, err
		}
	}
	if r.rowsFn != nil {
		return r.rowsFn(idx), nil
	}
	return 0, nil
}

func (r *recordingExecer) callsCopy() []retentionCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]retentionCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestRetentionOnce_ReportsRowCount(t *testing.T) {
	// Two batches: first is full (10 000), second is a short read (100).
	r := &recordingExecer{
		rowsFn: func(i int) int64 {
			if i == 0 {
				return RetentionBatchSize
			}
			return 100
		},
	}
	got, err := RetentionOnce(context.Background(), r)
	if err != nil {
		t.Fatalf("RetentionOnce: %v", err)
	}
	if got != RetentionBatchSize+100 {
		t.Errorf("rows = %d, want %d (sum across batches)", got, RetentionBatchSize+100)
	}
	calls := r.callsCopy()
	if len(calls) != 2 {
		t.Errorf("calls = %d, want 2 (full batch + short read)", len(calls))
	}
	for i, c := range calls {
		if !strings.Contains(c.SQL, "13 months") {
			t.Errorf("call[%d] missing 13-month cutoff: %q", i, c.SQL)
		}
		if c.Args[0] != RetentionBatchSize {
			t.Errorf("call[%d] arg[0] = %v, want %d", i, c.Args[0], RetentionBatchSize)
		}
	}
}

func TestRetentionOnce_Idempotent(t *testing.T) {
	r := &recordingExecer{
		rowsFn: func(i int) int64 {
			if i == 0 {
				return RetentionBatchSize
			}
			return 0
		},
	}
	if _, err := RetentionOnce(context.Background(), r); err != nil {
		t.Fatalf("first RetentionOnce: %v", err)
	}
	got, err := RetentionOnce(context.Background(), r)
	if err != nil {
		t.Fatalf("second RetentionOnce: %v", err)
	}
	if got != 0 {
		t.Errorf("second call rows = %d, want 0 (idempotent)", got)
	}
}

func TestRetentionOnce_PropagatesError(t *testing.T) {
	want := errors.New("postgres dropped connection")
	r := &recordingExecer{errFn: func(int) error { return want }}
	_, err := RetentionOnce(context.Background(), r)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want wrap of %v", err, want)
	}
}

func TestRetentionOnce_HitsBatchCap(t *testing.T) {
	// Stub always returns a full batch — loop should hit
	// MaxRetentionBatches and return the sentinel.
	r := &recordingExecer{
		rowsFn: func(int) int64 { return RetentionBatchSize },
	}
	got, err := RetentionOnce(context.Background(), r)
	if !errors.Is(err, ErrRetentionBatchCap) {
		t.Errorf("err = %v, want ErrRetentionBatchCap", err)
	}
	if want := int64(MaxRetentionBatches) * RetentionBatchSize; got != want {
		t.Errorf("rows = %d, want %d (cap × batch size)", got, want)
	}
	if got := len(r.callsCopy()); got != MaxRetentionBatches {
		t.Errorf("calls = %d, want %d (one per batch)", got, MaxRetentionBatches)
	}
}

func TestRetentionOnce_ShortReadExits(t *testing.T) {
	r := &recordingExecer{
		rowsFn: func(int) int64 { return 5 },
	}
	got, err := RetentionOnce(context.Background(), r)
	if err != nil {
		t.Fatalf("RetentionOnce: %v", err)
	}
	if got != 5 {
		t.Errorf("rows = %d, want 5", got)
	}
	if got := len(r.callsCopy()); got != 1 {
		t.Errorf("calls = %d, want 1 (short read exits the loop)", got)
	}
}

func TestRetentionSQL_HasBoundedDeleteShape(t *testing.T) {
	must := []string{
		"DELETE FROM public.usage_minutes",
		"WHERE ctid IN (",
		"SELECT ctid FROM public.usage_minutes",
		"LIMIT $1",
		"interval '13 months'",
	}
	for _, want := range must {
		if !strings.Contains(retentionBatchSQL, want) {
			t.Errorf("retentionBatchSQL missing %q; an unbounded DELETE would balloon WAL on the EX44. Got:\n%s", want, retentionBatchSQL)
		}
	}
}

func TestRetentionLoop_StopsOnContextCancel(t *testing.T) {
	r := &recordingExecer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RetentionLoop(ctx, r, time.Hour, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not exit on ctx.Done()")
	}
}

func TestRetentionLoop_DefaultIntervalWhenZero(t *testing.T) {
	if DefaultRetentionInterval != 24*time.Hour {
		t.Errorf("DefaultRetentionInterval = %v, want 24h", DefaultRetentionInterval)
	}
}

func TestRetentionLoop_TicksAtLeastOnce(t *testing.T) {
	r := &recordingExecer{
		rowsFn: func(int) int64 { return 0 },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RetentionLoop(ctx, r, 10*time.Millisecond, nil)
		close(done)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not exit on ctx.Done()")
	}
	if got := len(r.callsCopy()); got < 1 {
		t.Errorf("calls = %d, want ≥ 1 (loop must tick at least once)", got)
	}
}

// TestRetentionOnceDeploymentAudit_SQLShape pins the bounded-
// DELETE shape against deployment_audit + the 90-day cutoff
// (SAFE-RELEASES production-leveling Stream D, issue #976 /
// ADR-122). A refactor that drops the ctid LIMIT or widens the
// cutoff must fail this test.
func TestRetentionOnceDeploymentAudit_SQLShape(t *testing.T) {
	must := []string{
		"DELETE FROM public.deployment_audit",
		"WHERE ctid IN (",
		"SELECT ctid FROM public.deployment_audit",
		"LIMIT $2",
	}
	for _, want := range must {
		if !strings.Contains(retentionDeploymentAuditBatchSQL, want) {
			t.Errorf("retentionDeploymentAuditBatchSQL missing %q; an unbounded DELETE would balloon WAL. Got:\n%s", want, retentionDeploymentAuditBatchSQL)
		}
	}
	// 90-day cutoff is bound via $1 (interval) + RetentionBatchSize via $2.
	// Pin both args so a refactor that swaps the parameter order is caught.
	r := &recordingExecer{
		rowsFn: func(int) int64 { return 7 },
	}
	got, err := RetentionOnceDeploymentAudit(context.Background(), r)
	if err != nil {
		t.Fatalf("RetentionOnceDeploymentAudit: %v", err)
	}
	if got != 7 {
		t.Errorf("rows = %d, want 7", got)
	}
	calls := r.callsCopy()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1 (short read exits the loop)", len(calls))
	}
	if got, want := calls[0].Args[0], fmt.Sprintf("%d days", DefaultDeploymentAuditRetentionDays); got != want {
		t.Errorf("arg[0] (interval) = %q, want %q", got, want)
	}
	if calls[0].Args[1] != RetentionBatchSize {
		t.Errorf("arg[1] = %v, want %d", calls[0].Args[1], RetentionBatchSize)
	}
	if DefaultDeploymentAuditRetentionDays != 90 {
		t.Errorf("DefaultDeploymentAuditRetentionDays = %d, want 90", DefaultDeploymentAuditRetentionDays)
	}
	if DefaultDeploymentAuditRetentionInterval != 6*time.Hour {
		t.Errorf("DefaultDeploymentAuditRetentionInterval = %v, want 6h", DefaultDeploymentAuditRetentionInterval)
	}
}

// TestRetentionLoopDeploymentAudit_OnTickRowsCallback pins the
// counter-callback contract: each tick that deletes >0 rows
// fires onTickRows exactly once with the cumulative count.
// Idle passes (n==0) and nil callbacks must not panic.
func TestRetentionLoopDeploymentAudit_OnTickRowsCallback(t *testing.T) {
	r := &recordingExecer{
		rowsFn: func(int) int64 { return 3 },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu         sync.Mutex
		calledWith []int64
	)
	done := make(chan struct{})
	go func() {
		RetentionLoopDeploymentAudit(ctx, r, 10*time.Millisecond, nil, func(n int64) {
			mu.Lock()
			calledWith = append(calledWith, n)
			mu.Unlock()
		}, nil)
		close(done)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(calledWith) < 1 {
		t.Fatalf("callback fired %d times, want ≥ 1", len(calledWith))
	}
	for _, n := range calledWith {
		if n <= 0 {
			t.Errorf("callback arg = %d, want > 0", n)
		}
	}
}

// TestRetentionLoopDeploymentAudit_NilCallbackSafe pins that a
// nil onTickRows doesn't panic — production wiring in cmd/
// meterd passes the closure, but the test seam must accept nil
// so callers without a Prometheus registry can wire the loop.
func TestRetentionLoopDeploymentAudit_NilCallbackSafe(t *testing.T) {
	r := &recordingExecer{
		rowsFn: func(int) int64 { return 5 },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		RetentionLoopDeploymentAudit(ctx, r, 10*time.Millisecond, nil, nil, nil)
		close(done)
	}()
	time.Sleep(35 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not exit on ctx.Done() with nil callback")
	}
}

// TestRetentionLoopDeploymentAudit_OnTickErrorBumpsCounter
// (SAFE-RELEASES-OBS PR-A) pins the failure-path callback
// contract: when RetentionOnceDeploymentAudit returns an error
// other than ErrRetentionBatchCap, onTickError fires once per
// failed pass so cmd/meterd can bump
// deployment_audit_gc_failed_total. PR-B's
// deployment_audit_gc_failing alert queries the counter's rate
// over a 1h window; pre-PR the failure was journal-only.
func TestRetentionLoopDeploymentAudit_OnTickErrorBumpsCounter(t *testing.T) {
	want := errors.New("simulated postgres outage")
	r := &recordingExecer{
		rowsFn: func(int) int64 { return 0 },
		errFn:  func(int) error { return want },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu       sync.Mutex
		gotErrs  []error
		firedCnt int
	)
	done := make(chan struct{})
	go func() {
		RetentionLoopDeploymentAudit(ctx, r, 10*time.Millisecond, nil, nil, func(err error) {
			mu.Lock()
			gotErrs = append(gotErrs, err)
			firedCnt++
			mu.Unlock()
		})
		close(done)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if firedCnt < 1 {
		t.Fatalf("onTickError fired %d times, want ≥ 1", firedCnt)
	}
	for _, e := range gotErrs {
		if !errors.Is(e, want) {
			t.Errorf("callback err = %v, want %v", e, want)
		}
	}
}
