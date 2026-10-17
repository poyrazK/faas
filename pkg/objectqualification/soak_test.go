package objectqualification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"golang.org/x/time/rate"
)

// adr: 1557
func TestSoakBounds(t *testing.T) {
	base := DefaultSoakOptions()
	base.Daemon, base.Control, base.Directory = "/bin/true", "/bin/true", filepath.Join(t.TempDir(), "new")
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Options){
		func(o *Options) { o.DiskBudgetBytes = 0 },
		func(o *Options) { o.DiskBudgetBytes = api.ObjectQualificationDiskBudget + 1 },
		func(o *Options) { o.SoakInterval = api.ObjectQualificationSoakMinInterval - 1 },
		func(o *Options) { o.SoakInterval = api.ObjectQualificationSoakMaxInterval + 1 },
		func(o *Options) { o.SoakFaultDuration = api.ObjectQualificationSoakMinFault - 1 },
		func(o *Options) { o.SoakFaultDuration = api.ObjectQualificationSoakMaxFault + 1 },
		func(o *Options) { o.Duration = o.SoakFaultDuration },
		func(o *Options) { o.MaxWorkflows = api.ObjectQualificationSoakMaxFlows + 1 },
		func(o *Options) { o.SoakWorkflowsPerSecond = -1 },
		func(o *Options) { o.SoakWorkflowsPerSecond = api.ObjectQualificationSoakMaxFlowsPerSecond + 1 },
		func(o *Options) { o.Cycles = 2; o.MaxWorkflows = api.ObjectQualificationSoakMaxFlows },
	} {
		o := base
		mutate(&o)
		if err := o.validate(); err == nil {
			t.Fatal("unbounded soak accepted", o)
		}
	}
	base.Soak, base.SoakFaults = false, false
	if err := base.validate(); err == nil {
		t.Fatal("ordinary load inherited the larger soak workflow ceiling")
	}
}

func TestSoakWindowCountsAreLosslessDuringConcurrentRecording(t *testing.T) {
	m := &measurements{}
	m.beginWindows()
	started := time.Now()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 1000 {
				m.record("put", started, 17, false, nil)
				m.record("get", started, 0, true, nil)
				m.record("get", started, 0, false, io.ErrUnexpectedEOF)
			}
		})
	}
	totals := map[string]OperationStats{}
	add := func(r LoadReport) {
		for name, stats := range r.Operations {
			if stats.P50MS > stats.P95MS || stats.P95MS > stats.P99MS || stats.P99MS > stats.MaxMS {
				t.Fatal("inconsistent interval quantiles", stats)
			}
			x := totals[name]
			x.Attempts += stats.Attempts
			x.Successes += stats.Successes
			x.ExpectedRejections += stats.ExpectedRejections
			x.Failures += stats.Failures
			x.PayloadBytes += stats.PayloadBytes
			totals[name] = x
		}
	}
	for range 20 {
		add(m.takeWindow(0, started, 0))
	}
	workers.Wait()
	add(m.takeWindow(0, started, 0))
	aggregate := m.finish(0, started, 0, "duration")
	for name, stats := range aggregate.Operations {
		if x := totals[name]; x.Attempts != stats.Attempts || x.Successes != stats.Successes || x.Failures != stats.Failures || x.ExpectedRejections != stats.ExpectedRejections || x.PayloadBytes != stats.PayloadBytes {
			t.Fatal("intervals lost or duplicated operations", name, x, stats)
		}
	}
	if aggregate.Operations["put"].Successes != 4000 || aggregate.Operations["get"].Failures != 4000 {
		t.Fatal(aggregate)
	}
}

func TestSoakRequiresFreshSuccessfulDiskObservation(t *testing.T) {
	l := &lab{options: DefaultSoakOptions()}
	if l.checkSoakAdmission() == nil {
		t.Fatal("missing observation admitted work")
	}
	for _, sample := range []*ResourceSample{
		{ObservedAt: time.Now(), DiskOK: false},
		{ObservedAt: time.Now().Add(-2 * api.ObjectQualificationSoakMaxSampleAge), DiskOK: true},
		{ObservedAt: time.Now(), DiskOK: true, DiskBytes: l.options.DiskBudgetBytes},
	} {
		l.latestSample.Store(sample)
		if l.checkSoakAdmission() == nil {
			t.Fatal("unsafe observation admitted work", sample)
		}
	}
	l.latestSample.Store(&ResourceSample{ObservedAt: time.Now(), DiskOK: true, DiskBytes: 100})
	if err := l.checkSoakAdmission(); err != nil {
		t.Fatal(err)
	}
}

func TestSoakTimelineKeepsIntervalCountersAndRefusesReplacement(t *testing.T) {
	l := &lab{root: t.TempDir(), options: DefaultSoakOptions()}
	l.options.SoakFaults = false
	l.primaryGeneration.Store(1)
	l.latestSample.Store(&ResourceSample{ObservedAt: time.Now(), DiskOK: true, MetricsOK: true, PrimaryGeneration: 1, CompactionCycles: 3, CompactionRecords: 100, CompactionBlobs: 10})
	m, counts := &measurements{}, &loadCounts{}
	monitor, err := l.startSoak(t.Context(), 0, time.Now().Add(-time.Second), m, counts, func() {})
	if err != nil {
		t.Fatal(err)
	}
	m.record("put", time.Now(), 123, false, nil)
	counts.completed.Store(1)
	l.latestSample.Store(&ResourceSample{ObservedAt: time.Now(), DiskOK: true, MetricsOK: true, PrimaryGeneration: 1, CompactionCycles: 5, CompactionRecords: 120, CompactionBlobs: 14})
	report, err := monitor.finish()
	if err != nil || report.Windows != 1 || report.CompactionCycles != 2 || report.CompactionRecords != 20 || report.CompactionBlobs != 4 {
		t.Fatal(report, err)
	}
	body, err := os.ReadFile(report.Timeline)
	var window SoakWindow
	if err != nil || json.Unmarshal(body, &window) != nil || !window.Final || window.WorkflowsTotal != 1 || window.Operations["put"].PayloadBytes != 123 || !window.CleanupRatesValid || window.CleanupRecordsPerSecond <= 0 {
		t.Fatal(string(body), err)
	}
	info, err := os.Stat(report.Timeline)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("timeline permissions", info, err)
	}
	if _, err = l.startSoak(t.Context(), 0, time.Now(), m, counts, func() {}); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing timeline accepted", err)
	}
}

func TestSoakCancellationFlushesAndJoinsMonitor(t *testing.T) {
	l := &lab{root: t.TempDir(), options: DefaultSoakOptions()}
	l.options.SoakFaults = false
	ctx, cancel := context.WithCancel(t.Context())
	m := &measurements{}
	workCanceled := make(chan struct{})
	var once sync.Once
	monitor, err := l.startSoak(ctx, 0, time.Now(), m, &loadCounts{}, func() { once.Do(func() { close(workCanceled) }) })
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-workCanceled:
	case <-time.After(time.Second):
		t.Fatal("soak monitor did not cancel workers")
	}
	// A canceled request can finish recording after the monitor sees its error.
	// The final interval must wait for the caller's worker drain signal.
	m.record("put", time.Now(), 0, false, context.Canceled)
	finished := make(chan soakResult, 1)
	go func() { report, err := monitor.finish(); finished <- soakResult{report: report, err: err} }()
	select {
	case result := <-finished:
		if !errors.Is(result.err, context.Canceled) || result.report.Windows != 1 {
			t.Fatal(result)
		}
		body, readErr := os.ReadFile(result.report.Timeline)
		var window SoakWindow
		if readErr != nil || json.Unmarshal(body, &window) != nil || window.Operations["put"].Failures != 1 {
			t.Fatal("late canceled request missing from final window", string(body), readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("soak monitor survived cancellation")
	}
	if err := monitor.file.Sync(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("timeline was not closed", err)
	}
}

func TestSoakPacingKeepsFinalAdmissionGapAndCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	limiter := rate.NewLimiter(1, 1)
	if !waitSoakPacing(ctx, limiter) {
		t.Fatal("initial paced request refused")
	}
	if waitSoakPacing(ctx, limiter) || ctx.Err() == nil {
		t.Fatal("pacing closed admission before its requested deadline")
	}
}

func TestSoakCompactionCounterRegressionIsNotCountedAsProgress(t *testing.T) {
	l := &lab{}
	l.primaryGeneration.Store(1)
	m := &soakMonitor{lab: l, baseline: &ResourceSample{MetricsOK: true, PrimaryGeneration: 1}}
	frame := ResourceSample{MetricsOK: true, ObservedAt: time.Now(), PrimaryGeneration: 1, CompactionCycles: 3, CompactionRecords: 30}
	if err := m.observeCompaction(&frame); err != nil {
		t.Fatal(err)
	}
	frame.CompactionRecords = 20
	if err := m.observeCompaction(&frame); err == nil {
		t.Fatal("regressed counter accepted")
	}
	frame.MetricsOK = false
	if err := m.observeCompaction(&frame); err != nil || m.summary.CompactionRecords != 30 {
		t.Fatal("failed probe altered observed cleanup", m.summary, err)
	}
}

func TestSoakTimelineBudgetAndCoverageFailClosed(t *testing.T) {
	l := &lab{root: t.TempDir(), options: DefaultSoakOptions()}
	file, err := os.CreateTemp(l.root, "bounded")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	m := &soakMonitor{lab: l, file: file, bytes: api.ObjectQualificationSoakTimelineBytes - 1, counts: &loadCounts{}, measure: &measurements{}, started: time.Now(), last: time.Now()}
	if err = m.writeWindow(false); err == nil {
		t.Fatal("timeline exceeded its output budget")
	}
	info, err := file.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("oversized interval partially written", info, err)
	}
	good := SoakReport{FullDuration: true, MetricsWindows: 1, CompactionCycles: 2, Interruptions: []SoakInterruption{{Recovered: true, LiveTraffic: true, PublishedBefore: 1, PublishedDuring: 1}, {Recovered: true, LiveTraffic: true, PublishedBefore: 2, PublishedDuring: 3}}}
	if err = validateSoakCoverage(&good, l.options); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SoakReport{{}, {FullDuration: true}, {FullDuration: true, MetricsWindows: 1}, {FullDuration: true, MetricsWindows: 1, CompactionCycles: 2}} {
		if validateSoakCoverage(&bad, l.options) == nil {
			t.Fatal("incomplete soak counted as passing", bad)
		}
	}
}

func TestSoakCustomerTrafficIncludesBodiesAndCanceledRequests(t *testing.T) {
	l := &lab{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "bytes")
	}))
	defer server.Close()
	client := &http.Client{Transport: customerTransport{next: http.DefaultTransport, lab: l}}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil || l.customerRequests.Load() != 1 || l.customerActive.Load() != 1 {
		t.Fatal("response body was not counted as active", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	_ = response.Body.Close()
	if l.customerActive.Load() != 0 {
		t.Fatal("body close lost activity accounting")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/wait", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || l.customerRequests.Load() != 2 || l.customerActive.Load() != 0 {
		t.Fatal("canceled request leaked activity", err)
	}
}

func TestSoakRestartedFollowerOutlivesInterruptionContext(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "primary")
	store, err := objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBucket("physical"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	head, err := objectnode.EnableReplication(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	head, err = objectnode.RegisterFollower(t.Context(), directory, uuid.NewString(), 0, head.AppliedHash)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	proxy := newFaultProxy()
	defer proxy.server.Close()
	defer proxy.transport.CloseIdleConnections()
	proxy.target.Store(server.URL)
	l := &lab{root: t.TempDir(), options: DefaultSoakOptions(), mode: objectnode.DurabilityLocal, proxy: proxy, key: "synthetic", secret: "isolated"}
	l.options.Control = filepath.Join(l.root, "follower-helper")
	l.options.SoakFaultDuration = api.ObjectQualificationSoakMinFault
	if err = os.WriteFile(l.options.Control, []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	l.customerActive.Store(1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err = l.startFollower(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.stopFollower(ctx, true) }()
	result, err := l.soakInterruption(ctx, 1, time.Now(), &loadCounts{})
	if err != nil || !result.Recovered {
		t.Fatal(result, err)
	}
	select {
	case <-l.follower.done:
		t.Fatal("interruption cleanup killed the restarted follower", l.follower.err)
	case <-time.After(100 * time.Millisecond):
	}
}

type soakPositionSource struct{ head objectnode.ReplicationStatus }

func (s soakPositionSource) Head(context.Context) (objectnode.ReplicationStatus, error) {
	return s.head, nil
}
func (soakPositionSource) Entry(context.Context, string, int64) (objectnode.JournalEntry, error) {
	return objectnode.JournalEntry{}, errors.New("unexpected entry request")
}
func (soakPositionSource) Blob(context.Context, string, string, int64, int64) (io.ReadCloser, error) {
	return nil, errors.New("unexpected blob request")
}

func TestSoakRejoinUsesFinitePositionAndReplicaConfirmation(t *testing.T) {
	head := objectnode.ReplicationStatus{AppliedSequence: 100, Followers: []objectnode.RegisteredFollower{{Sequence: 20}}, Durability: &objectnode.ReplicaDurability{ConfirmedSequence: 20}}
	l := &lab{mode: objectnode.DurabilityReplica}
	if err := l.waitReplicaThrough(t.Context(), soakPositionSource{head}, 20); err != nil {
		t.Fatal("rejoin incorrectly waited for advancing head", err)
	}
	head.Durability.ConfirmedSequence = 19
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := l.waitReplicaThrough(ctx, soakPositionSource{head}, 20); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("replica confirmation bypassed", err)
	}
}
