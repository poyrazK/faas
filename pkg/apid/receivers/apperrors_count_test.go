package receivers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
)

type transientIssueStore struct {
	*countingAppErrorsStore
	state.IssueStore
	attempts atomic.Int32
	events   chan string
}

func (s *transientIssueStore) IssueResourceAccount(ctx context.Context, _ string, account string) (state.Account, error) {
	return s.AccountByID(ctx, account)
}

func (s *transientIssueStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Plan: api.PlanHobby, Status: state.AccountActive}, nil
}
func (s *transientIssueStore) RecordIssue(_ context.Context, in state.RecordIssueParams) (api.IssueEventResponse, error) {
	s.events <- in.Event.EventID
	if s.attempts.Add(1) == 1 {
		return api.IssueEventResponse{}, errors.New("temporary database failure")
	}
	return api.IssueEventResponse{}, nil
}

func TestHTTPFailureRetriesIssueWithoutRepeatingLegacyCount(t *testing.T) {
	store := &transientIssueStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}, events: make(chan string, 2)}
	receiver := NewAppErrorsReceiver(store, nil, true)
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli(), IssueEventId: uuid.NewString(), RequestRowOmitted: true}
	req.Count = 3
	req.IssueTimeBuckets = make([]uint32, 1000)
	req.IssueTimeBuckets[req.ReceivedAtUnixMs%1000] = 3
	out := receiver.HandleOne(context.Background(), req)
	if out.GetOutcome() != OutcomeInserted {
		t.Fatalf("legacy outcome: %s", out.GetOutcome())
	}
	first := <-store.events
	req.IssueEventId = uuid.NewString() // retry owns an immutable snapshot
	req.IssueTimeBuckets[req.ReceivedAtUnixMs%1000] = 2
	select {
	case second := <-store.events:
		if first != second || len(store.increments) != 1 {
			t.Fatalf("retry event IDs %s/%s; legacy increments=%d", first, second, len(store.increments))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("issue retry did not run")
	}
}

func TestHTTPFailureRetryStopsOnPermanentRejection(t *testing.T) {
	calls := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := retryHTTPIssueUntil(ctx, func(context.Context) error {
		calls++
		return state.ErrInvalidArgument
	}, time.Millisecond, 10*time.Millisecond)
	if !errors.Is(err, state.ErrInvalidArgument) || calls != 1 {
		t.Fatalf("permanent error retried: calls=%d err=%v", calls, err)
	}
}

func TestHTTPIssueRetryWaitsForPerAppSlot(t *testing.T) {
	receiver := NewAppErrorsReceiver(nil, nil, true)
	app := uuid.NewString()
	for i := 0; i < issueRetryPerApp; i++ {
		if !receiver.acquireHTTPIssueRetry(context.Background(), app) {
			t.Fatal("initial retry slot unavailable")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	acquired := make(chan bool, 1)
	go func() { acquired <- receiver.acquireHTTPIssueRetry(ctx, app) }()
	deadline := time.Now().Add(time.Second)
	for {
		receiver.issueRetryMu.Lock()
		waiting := receiver.issueRetryWaitApps[app]
		receiver.issueRetryMu.Unlock()
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not wait for a slot")
		}
		time.Sleep(time.Millisecond)
	}
	receiver.releaseHTTPIssueRetry(app)
	if !<-acquired {
		t.Fatal("waiting retry was dropped instead of admitted")
	}
	for i := 0; i < issueRetryPerApp; i++ {
		receiver.releaseHTTPIssueRetry(app)
	}
}

func TestHTTPFailureReportsFullIssueQueue(t *testing.T) {
	store := &countingAppErrorsStore{plan: api.PlanHobby}
	receiver := NewAppErrorsReceiver(store, nil, true)
	app := uuid.NewString()
	receiver.issueAttemptApps[app] = issueRetryPerApp
	receiver.issuePendingApps[app] = issuePendingPerApp
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: app, DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), IssueEventId: uuid.NewString(), RouteTemplate: "/orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli(), RequestRowOmitted: true}
	out := receiver.HandleOne(context.Background(), req)
	if out.GetOutcome() != OutcomeInserted || out.GetIssueHandoff() != "retry" || len(store.increments) != 1 {
		t.Fatalf("legacy write and independent retry signal: %+v", out)
	}
}

func TestHTTPFailureAttemptsUseSharedIngestGate(t *testing.T) {
	store := &issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}, events: make(chan struct{}, 1)}
	receiver := NewAppErrorsReceiver(store, nil, true)
	receiver.gate = NewIngestGate(1)
	release, err := receiver.gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), IssueEventId: uuid.NewString(), RouteTemplate: "/orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli()}
	if got := receiver.enqueueHTTPIssue(req); got != "queued" {
		t.Fatalf("handoff = %q", got)
	}
	select {
	case <-store.events:
		t.Fatal("issue write bypassed the held ingest gate")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	release = func() {}
	select {
	case <-store.events:
	case <-time.After(time.Second):
		t.Fatal("issue write did not start after gate release")
	}
}

type issueCaptureStore struct {
	*countingAppErrorsStore
	state.IssueStore
	mu       sync.Mutex
	recorded int
	eventIDs []string
	counts   []int64
	times    [][][2]int64
	events   chan struct{}
}

func (s *issueCaptureStore) IssueResourceAccount(ctx context.Context, _ string, account string) (state.Account, error) {
	return s.AccountByID(ctx, account)
}

func (s *issueCaptureStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Plan: api.PlanHobby, Status: state.AccountActive}, nil
}

func (s *issueCaptureStore) RecordIssue(_ context.Context, in state.RecordIssueParams) (api.IssueEventResponse, error) {
	s.mu.Lock()
	s.recorded++
	s.eventIDs = append(s.eventIDs, in.Event.EventID)
	s.counts = append(s.counts, in.OccurrenceCount)
	s.times = append(s.times, append([][2]int64(nil), in.TimeBuckets...))
	s.mu.Unlock()
	if s.events != nil {
		s.events <- struct{}{}
	}
	return api.IssueEventResponse{}, nil
}

func TestHTTPFailureFirstAttemptIsIndependentOfRetrySlots(t *testing.T) {
	store := &issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}, events: make(chan struct{}, 1)}
	receiver := NewAppErrorsReceiver(store, nil, true)
	app := uuid.NewString()
	// Model an app whose old failures are all waiting for retries.
	receiver.issueRetryCount = issueRetryCapacity
	receiver.issueRetryApps[app] = issueRetryPerApp
	receiver.enqueueHTTPIssue(&apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: app, DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), IssueEventId: uuid.NewString(), RouteTemplate: "/orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli(), Count: 42})
	select {
	case <-store.events:
	case <-time.After(3 * time.Second):
		t.Fatal("new issue was not attempted while retry slots were full")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.counts) != 1 || store.counts[0] != 42 {
		t.Fatalf("gateway count was not preserved: %v", store.counts)
	}
}

func TestRecordHTTPIssueDoesNotReplayLegacyCounts(t *testing.T) {
	store := &issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}}
	receiver := NewAppErrorsReceiver(store, nil, true)
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), IssueEventId: uuid.NewString(), RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli()}
	if _, err := receiver.RecordHTTPIssue(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(store.increments) != 0 || len(store.eventIDs) != 1 || store.eventIDs[0] != req.IssueEventId {
		t.Fatalf("issue retry changed legacy counts or event identity: increments=%d events=%v", len(store.increments), store.eventIDs)
	}
}

func TestHTTPFailuresUseStableGatewayEventIdentity(t *testing.T) {
	store := &issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}}
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: "reused-request-id", RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli(), IssueEventId: uuid.NewString()}
	if err := RecordHTTPIssue(context.Background(), store, req); err != nil {
		t.Fatal(err)
	}
	if err := RecordHTTPIssue(context.Background(), store, req); err != nil {
		t.Fatal(err)
	}
	req.IssueEventId = uuid.NewString()
	if err := RecordHTTPIssue(context.Background(), store, req); err != nil {
		t.Fatal(err)
	}
	if len(store.eventIDs) != 3 || store.eventIDs[0] != store.eventIDs[1] || store.eventIDs[1] == store.eventIDs[2] {
		t.Fatalf("incorrect HTTP event identities: %v", store.eventIDs)
	}
}

func TestHTTPFailuresReachIssuesWithoutLegacyRequestSample(t *testing.T) {
	store := &issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}, events: make(chan struct{}, 2)}
	receiver := NewAppErrorsReceiver(store, nil, true)
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli()}
	req.RequestRowOmitted = true
	receiver.HandleOne(context.Background(), req)
	store.kept = int64(api.PlanHobby.AppErrorsMaxRequestRowsPerFingerprint())
	req.RequestRowOmitted = false
	receiver.HandleOne(context.Background(), req)
	for i := 0; i < 2; i++ {
		select {
		case <-store.events:
		case <-time.After(3 * time.Second):
			t.Fatal("HTTP issue was not recorded")
		}
	}
}

type countingAppErrorsStore struct {
	increments []sqlc.IncrementAppErrorParams
	kept       int64
	plan       api.Plan
}

func (s *countingAppErrorsStore) IncrementAppError(_ context.Context, arg sqlc.IncrementAppErrorParams) (bool, error) {
	s.increments = append(s.increments, arg)
	return len(s.increments) == 1, nil
}

func (s *countingAppErrorsStore) InsertAppErrorRequest(context.Context, sqlc.InsertAppErrorRequestParams) error {
	s.kept++
	return nil
}

func (s *countingAppErrorsStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Plan: s.plan}, nil
}

func (s *countingAppErrorsStore) CountAppErrorRequestsForFingerprint(context.Context, sqlc.CountAppErrorRequestsForFingerprintParams) (int64, error) {
	return s.kept, nil
}

// A gateway record stands for count requests, a count-only record stores no
// request row, and an error group stops gaining request rows at the plan's
// AppErrorsMaxRequestRowsPerFingerprint (bug hunt 2026-10-28 H41).
func TestAppErrorsReceiverCountsFoldedRequestsAndCapsRequestRows(t *testing.T) {
	store := &countingAppErrorsStore{plan: api.PlanFree}
	receiver := NewAppErrorsReceiver(store, nil, true)
	record := func(count int64, omitted bool) {
		t.Helper()
		out := receiver.HandleOne(context.Background(), &apidpb.IncrementAppErrorRequest{
			AccountId: uuid.NewString(), AppId: uuid.NewString(), RouteTemplate: "GET /wp-login.php",
			HttpStatus: 404, ErrorClass: "not_found", Fingerprint: strings.Repeat("a", 64),
			Count: count, RequestRowOmitted: omitted,
		})
		if out.GetOutcome() != OutcomeInserted && out.GetOutcome() != "merged" {
			t.Fatalf("outcome = %q", out.GetOutcome())
		}
	}

	record(0, false) // an older gateway: one request
	record(500, false)
	record(40, true)
	if len(store.increments) != 3 || store.increments[0].Count != 1 || store.increments[1].Count != 500 || store.increments[2].Count != 40 {
		t.Fatalf("increments = %+v, want counts 1, 500, 40", store.increments)
	}
	if store.kept != 2 {
		t.Fatalf("request rows = %d, want 2 (the count-only record stores none)", store.kept)
	}

	limit := int64(api.PlanFree.AppErrorsMaxRequestRowsPerFingerprint())
	for store.kept < limit+5 {
		before := store.kept
		record(1, false)
		if store.kept == before {
			break
		}
	}
	if store.kept != limit {
		t.Fatalf("request rows = %d, want the Free cap %d", store.kept, limit)
	}
}

type blockingIssueStore struct {
	*countingAppErrorsStore
	state.IssueStore
	started chan string
	release chan struct{}
}

func (s *blockingIssueStore) IssueResourceAccount(ctx context.Context, _ string, account string) (state.Account, error) {
	return s.AccountByID(ctx, account)
}

func (s *blockingIssueStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Plan: api.PlanHobby, Status: state.AccountActive}, nil
}

func (s *blockingIssueStore) RecordIssue(ctx context.Context, in state.RecordIssueParams) (api.IssueEventResponse, error) {
	s.started <- in.Credential.AppID
	select {
	case <-s.release:
		return api.IssueEventResponse{}, nil
	case <-ctx.Done():
		return api.IssueEventResponse{}, ctx.Err()
	}
}

func TestHTTPFailureDoesNotBlockLegacyCountingAndLimitsPerApp(t *testing.T) {
	store := &blockingIssueStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}, started: make(chan string, issueRetryCapacity), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(store.release) }) })
	receiver := NewAppErrorsReceiver(store, nil, true)
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli(), RequestRowOmitted: true}
	firstApp := req.AppId
	for i := 0; i < issueRetryPerApp+1; i++ {
		if out := receiver.HandleOne(context.Background(), req); out.GetOutcome() != OutcomeInserted && out.GetOutcome() != outcomeMerged {
			t.Fatalf("legacy outcome: %s", out.GetOutcome())
		}
	}
	req.AppId = uuid.NewString()
	receiver.HandleOne(context.Background(), req)
	seen := map[string]int{}
	for i := 0; i < issueRetryPerApp+1; i++ {
		select {
		case app := <-store.started:
			seen[app]++
		case <-time.After(3 * time.Second):
			t.Fatal("Issue attempts did not start")
		}
	}
	if seen[firstApp] != issueRetryPerApp || seen[req.AppId] != 1 || len(store.increments) != issueRetryPerApp+2 {
		t.Fatalf("issue slots=%v, legacy counts=%d", seen, len(store.increments))
	}
	release.Do(func() { close(store.release) })
	select {
	case app := <-store.started:
		if app != firstApp {
			t.Fatalf("queued wrong app: %s", app)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued issue never received its first attempt")
	}
}

func TestHTTPFailureRetryEligibility(t *testing.T) {
	if retryableHTTPIssueError(&state.IssueLimitError{Resource: "issues"}) || !retryableHTTPIssueError(&state.IssueLimitError{Resource: "events"}) || !retryableHTTPIssueError(&state.IssueLimitError{Resource: "events_per_minute", Rate: true}) || retryableHTTPIssueError(errHTTPIssueSkipped) {
		t.Fatal("incorrect retry eligibility")
	}
}

type suspendedIssueStore struct{ *issueCaptureStore }

func (s *suspendedIssueStore) IssueResourceAccount(ctx context.Context, _ string, account string) (state.Account, error) {
	return s.AccountByID(ctx, account)
}

func (s *suspendedIssueStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	return state.Account{ID: id, Plan: api.PlanHobby, Status: state.AccountSuspended}, nil
}

func TestHTTPIssueReportsSkippedAccountSeparately(t *testing.T) {
	store := &suspendedIssueStore{&issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}}}
	recorded, err := recordHTTPIssue(context.Background(), store, &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), HttpStatus: 500})
	if err != nil || recorded != httpIssueSkipped || store.recorded != 0 {
		t.Fatalf("suspended account: outcome=%v err=%v", recorded, err)
	}
}

type duplicateIssueStore struct{ *issueCaptureStore }

func (s *duplicateIssueStore) RecordIssue(context.Context, state.RecordIssueParams) (api.IssueEventResponse, error) {
	return api.IssueEventResponse{Duplicate: true}, nil
}

func TestHTTPDuplicateIsCountedAsDuplicate(t *testing.T) {
	store := &duplicateIssueStore{&issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}}}
	ops := wire.NewOpsMetrics("http_issue_duplicate_test")
	receiver := NewAppErrorsReceiver(store, ops, true)
	receiver.enqueueHTTPIssue(&apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli()})
	deadline := time.Now().Add(3 * time.Second)
	for {
		families, err := ops.Registry().Gather()
		if err != nil {
			t.Fatal(err)
		}
		var duplicate, accepted float64
		for _, family := range families {
			if family.GetName() != "http_issue_duplicate_test_issue_events_recorded_total" {
				continue
			}
			for _, metric := range family.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "outcome" && label.GetValue() == "duplicate" {
						duplicate += metric.GetCounter().GetValue()
					}
					if label.GetName() == "outcome" && label.GetValue() == "accepted" {
						accepted += metric.GetCounter().GetValue()
					}
				}
			}
		}
		if duplicate == 1 && accepted == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("duplicate=%v accepted=%v", duplicate, accepted)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAppErrorsReceiverBoundsLegacyRedactedSample(t *testing.T) {
	store := &countingAppErrorsStore{plan: api.PlanHobby}
	receiver := NewAppErrorsReceiver(store, nil, true)
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), RequestId: uuid.NewString(), RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), SampleMessage: strings.Repeat("x", 515), ReceivedAtUnixMs: time.Now().UnixMilli(), RequestRowOmitted: true}
	out := receiver.HandleOne(context.Background(), req)
	if out.GetOutcome() != OutcomeInserted || len(store.increments) != 1 || len(store.increments[0].SampleMessage) != api.AppErrorsSampleMessageCapBytes-4 || len(req.SampleMessage) != 515 {
		t.Fatalf("legacy sample not bounded independently: outcome=%s increments=%+v original=%d", out.GetOutcome(), store.increments, len(req.SampleMessage))
	}
}

func TestHTTPFailureValidatesAndPreservesTimeBuckets(t *testing.T) {
	store := &issueCaptureStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanHobby}}
	sample := time.Now().UTC().Truncate(time.Second).Add(-time.Second).Add(100 * time.Millisecond)
	bins := make([]uint32, 1000)
	bins[100], bins[400] = 1, 4
	req := &apidpb.IncrementAppErrorRequest{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), IssueEventId: uuid.NewString(), RouteTemplate: "GET /orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: sample.UnixMilli(), Count: 5, IssueTimeBuckets: bins}
	if err := RecordHTTPIssue(context.Background(), store, req); err != nil {
		t.Fatal(err)
	}
	if len(store.times) != 1 || len(store.times[0]) != 2 || store.times[0][1] != [2]int64{sample.Add(300 * time.Millisecond).UnixMilli(), 4} {
		t.Fatalf("buckets=%v", store.times)
	}
	req.IssueTimeBuckets[400] = 3
	if err := RecordHTTPIssue(context.Background(), store, req); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("mismatched bucket count: %v", err)
	}
}

type wrappedIssueOwnerStore struct {
	*countingAppErrorsStore
	state.IssueStore
	app    state.App
	org    state.Org
	limits api.IssueLimits
}

func (s *wrappedIssueOwnerStore) AppByID(context.Context, string) (state.App, error) {
	return s.app, nil
}
func (s *wrappedIssueOwnerStore) OrgByID(context.Context, string) (state.Org, error) {
	return s.org, nil
}
func (s *wrappedIssueOwnerStore) RecordIssue(_ context.Context, in state.RecordIssueParams) (api.IssueEventResponse, error) {
	s.limits = in.Limits
	return api.IssueEventResponse{}, nil
}
func TestHTTPFailureUsesWrappedCompanyResourceOwner(t *testing.T) {
	accountID, appID, orgID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &wrappedIssueOwnerStore{countingAppErrorsStore: &countingAppErrorsStore{plan: api.PlanFree}, app: state.App{ID: appID, AccountID: accountID, OrgID: orgID}, org: state.Org{ID: orgID, Plan: api.PlanPro, Status: state.OrgStatusActive, ResourceOwnershipEnabled: true}}
	req := &apidpb.IncrementAppErrorRequest{AccountId: accountID, AppId: appID, DeploymentId: uuid.NewString(), RequestId: uuid.NewString(), IssueEventId: uuid.NewString(), RouteTemplate: "/orders", HttpStatus: 500, ErrorClass: "unhandled", Fingerprint: strings.Repeat("a", 64), ReceivedAtUnixMs: time.Now().UnixMilli()}
	outcome, err := recordHTTPIssue(context.Background(), store, req)
	if err != nil || outcome != httpIssueAccepted || store.limits != api.PlanPro.IssueLimits() {
		t.Fatalf("outcome=%v limits=%+v err=%v", outcome, store.limits, err)
	}
}
