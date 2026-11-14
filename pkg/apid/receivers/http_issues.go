package receivers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RecordHTTPIssue provides an idempotent retry path for an ambiguous legacy
// stream failure. It does not touch the non-idempotent app-error counters.
func (a *appErrorsReceiver) RecordHTTPIssue(ctx context.Context, req *apidpb.IncrementAppErrorRequest) (*apidpb.IncrementAppErrorResponse, error) {
	if !a.enabled {
		return nil, status.Error(codes.Unavailable, "app_errors disabled")
	}
	req = normalizeAppErrorSample(req)
	if err := validateIncrementRequest(req); err != nil {
		return nil, err
	}
	if req.GetHttpStatus() < 500 || req.GetDeploymentId() == "" || req.GetRequestId() == "" || req.GetIssueEventId() == "" {
		return nil, status.Error(codes.InvalidArgument, "HTTP issue identity required")
	}
	admitCtx, cancelAdmission := context.WithTimeout(ctx, 200*time.Millisecond)
	release, err := a.acquireIssueWrite(admitCtx, req.GetAppId())
	cancelAdmission()
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Error(codes.ResourceExhausted, "HTTP issue admission busy")
	}
	defer release()
	writeCtx, cancelWrite := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWrite()
	outcome, err := recordHTTPIssue(writeCtx, a.store, req)
	if err != nil {
		if retryableHTTPIssueError(err) {
			return nil, status.Error(codes.Unavailable, "HTTP issue recording unavailable")
		}
		return nil, status.Error(codes.InvalidArgument, "HTTP issue rejected")
	}
	result := "accepted"
	switch outcome {
	case httpIssueDuplicate:
		result = "duplicate"
	case httpIssueSkipped:
		result = "retry_skipped"
	}
	a.ops.ObserveIssueEvent(result)
	return &apidpb.IncrementAppErrorResponse{Outcome: result}, nil
}

// HTTP grouping is a separate source kind: observing a 500 does not prove an
// application exception. Ordinary client errors remain in the existing API.
func RecordHTTPIssue(ctx context.Context, store AppErrorsStore, req *apidpb.IncrementAppErrorRequest) error {
	_, err := recordHTTPIssue(ctx, store, req)
	return err
}

type httpIssueOutcome uint8

const (
	httpIssueSkipped httpIssueOutcome = iota
	httpIssueAccepted
	httpIssueDuplicate
)

// recordHTTPIssue distinguishes a deliberate skip, an accepted occurrence,
// and an idempotent duplicate.
func recordHTTPIssue(ctx context.Context, store AppErrorsStore, req *apidpb.IncrementAppErrorRequest) (httpIssueOutcome, error) {
	if req.GetHttpStatus() < 500 || req.GetDeploymentId() == "" {
		return httpIssueSkipped, nil
	}
	st, ok := store.(state.IssueStore)
	if !ok {
		return httpIssueSkipped, nil
	}
	var acct state.Account
	var err error
	if accounts, ok := store.(interface {
		IssueResourceAccount(context.Context, string, string) (state.Account, error)
	}); ok {
		acct, err = accounts.IssueResourceAccount(ctx, req.GetAppId(), req.GetAccountId())
	} else if reader, ok := store.(interface {
		state.AppOwnerReader
		AppByID(context.Context, string) (state.App, error)
	}); ok {
		// A state.Store wrapper may expose the standard owner reader without
		// forwarding the concrete store's convenience method.
		acct, err = state.IssueResourceAccount(ctx, reader, req.GetAppId(), req.GetAccountId())
	} else {
		return httpIssueSkipped, nil
	}
	if err != nil {
		return httpIssueSkipped, err
	}
	lim := acct.Plan.IssueLimits()
	if !lim.Enabled || !acct.Active() {
		return httpIssueSkipped, nil
	}
	requestID := req.GetRequestId()
	if requestID == "" {
		return httpIssueSkipped, nil
	}
	// New gateways assign a stable ID per record. Preserve legacy retry
	// identity for older gateways until they have upgraded.
	eventID := req.GetIssueEventId()
	if eventID == "" {
		eventID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("http:"+req.GetDeploymentId()+":"+requestID)).String()
	}
	event := api.IssueEvent{EventID: eventID, OccurredAt: time.UnixMilli(req.GetReceivedAtUnixMs()).UTC(), ExceptionType: req.GetErrorClass(), Message: req.GetSampleMessage(), Route: req.GetRouteTemplate(), HTTPStatus: int(req.GetHttpStatus()), RequestID: requestID, SourceKind: "http", FingerprintOverride: req.GetFingerprint()}
	count := max(int64(1), req.GetCount())
	if count > api.IssueHTTPBatchMaxCount {
		return httpIssueSkipped, state.ErrInvalidArgument
	}
	now := time.Now().UTC()
	buckets, err := httpIssueTimeBuckets(req, count, now)
	if err != nil {
		return httpIssueSkipped, fmt.Errorf("%w: %w", state.ErrInvalidArgument, err)
	}
	normalized, fp, title, err := issues.Normalize(event, now, lim)
	if err != nil {
		return httpIssueSkipped, fmt.Errorf("%w: %w", state.ErrInvalidArgument, err)
	}
	if len(buckets) != 0 && normalized.OccurredAt.Before(event.OccurredAt) {
		normalized.OccurredAt = normalized.OccurredAt.Truncate(time.Millisecond)
	}
	digest := issues.PayloadDigest(event)
	if count > 1 {
		digest = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", digest, count))))
	}
	if len(buckets) != 0 {
		// Hash the original wire buckets so a retry after clock clamping remains stable.
		raw, _ := json.Marshal(req.GetIssueTimeBuckets())
		digest = fmt.Sprintf("%x", sha256.Sum256(append([]byte(digest+":"), raw...)))
	}
	out, err := st.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: req.GetAccountId(), AppID: req.GetAppId(), DeploymentID: req.GetDeploymentId(), Environment: "application"}, Event: normalized, Fingerprint: fp, Title: title, PayloadHash: digest, GroupingVersion: issues.GroupingVersion, OccurrenceCount: count, TimeBuckets: buckets, Limits: lim, Now: now, EnforceResourcePolicy: true})
	if err != nil {
		return httpIssueSkipped, err
	}
	if out.Duplicate {
		return httpIssueDuplicate, nil
	}
	return httpIssueAccepted, nil
}

// A gateway supplies 1,000 millisecond counters for one UTC second. Validate
// both the total and the sample, and clamp future times just as Normalize does.
func httpIssueTimeBuckets(req *apidpb.IncrementAppErrorRequest, count int64, now time.Time) ([][2]int64, error) {
	raw := req.GetIssueTimeBuckets()
	if len(raw) == 0 {
		return nil, nil
	}
	sample := req.GetReceivedAtUnixMs()
	if len(raw) != 1000 || sample <= 0 || raw[sample%1000] == 0 {
		return nil, fmt.Errorf("invalid HTTP time buckets")
	}
	base := sample - sample%1000
	var total int64
	var buckets [][2]int64
	for i, n := range raw {
		if n == 0 {
			continue
		}
		ms := base + int64(i)
		if time.UnixMilli(ms).After(now.Add(api.IssueMaxClockSkew)) {
			return nil, fmt.Errorf("HTTP time bucket exceeds clock skew")
		}
		if ms > now.UnixMilli() {
			ms = now.UnixMilli()
		}
		total += int64(n)
		if len(buckets) > 0 && buckets[len(buckets)-1][0] == ms {
			buckets[len(buckets)-1][1] += int64(n)
		} else {
			buckets = append(buckets, [2]int64{ms, int64(n)})
		}
	}
	if total != count {
		return nil, fmt.Errorf("HTTP time bucket total does not match count")
	}
	return buckets, nil
}
