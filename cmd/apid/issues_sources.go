package main

import (
	"context"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

// HTTP grouping is a separate source kind: observing a 500 does not prove an
// application exception. Ordinary client errors remain in the existing API.
func recordHTTPIssue(ctx context.Context, store appErrorsStore, req *apidpb.IncrementAppErrorRequest) error {
	if req.GetHttpStatus() < 500 || req.GetDeploymentId() == "" {
		return nil
	}
	st, ok := store.(state.IssueStore)
	if !ok {
		return nil
	}
	accounts, ok := store.(interface {
		AccountByID(context.Context, string) (state.Account, error)
	})
	if !ok {
		return nil
	}
	acct, err := accounts.AccountByID(ctx, req.GetAccountId())
	if err != nil {
		return err
	}
	lim := acct.Plan.IssueLimits()
	if !lim.Enabled || !acct.Active() {
		return nil
	}
	requestID := req.GetRequestId()
	if requestID == "" {
		return nil
	}
	// Include deployment in the transport event ID so mirrored calls with an
	// inherited request ID do not collide. The issue fingerprint excludes it.
	event := api.IssueEvent{EventID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("http:"+req.GetDeploymentId()+":"+requestID)).String(), OccurredAt: time.UnixMilli(req.GetReceivedAtUnixMs()).UTC(), ExceptionType: req.GetErrorClass(), Message: req.GetSampleMessage(), Route: req.GetRouteTemplate(), HTTPStatus: int(req.GetHttpStatus()), RequestID: requestID, SourceKind: "http", FingerprintOverride: req.GetFingerprint()}
	now := time.Now().UTC()
	normalized, fp, title, err := issues.Normalize(event, now, lim)
	if err != nil {
		return err
	}
	_, err = st.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: req.GetAccountId(), AppID: req.GetAppId(), DeploymentID: req.GetDeploymentId(), Environment: "application"}, Event: normalized, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(event), GroupingVersion: issues.GroupingVersion, Limits: lim, Now: now})
	return err
}
