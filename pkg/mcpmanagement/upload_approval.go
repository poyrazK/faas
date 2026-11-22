package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type pendingUploadApproval struct {
	uploadID string
	info     api.AgentApprovalPendingInfo
}

// retainUploadApproval extends a hosted plan only for its native,
// body-bound upload commit approval. Caller holds the plan and source pinned.
func (m *management) retainUploadApproval(ctx context.Context, p *releasePlan, applyErr error) error {
	var apiErr *api.APIError
	if !p.LocalSource.Uploaded || m.opts.uploads == nil || !errors.As(applyErr, &apiErr) ||
		apiErr.Problem.Status != http.StatusPreconditionRequired || apiErr.Problem.Code != api.CodeApprovalRequired {
		return applyErr
	}
	if p.uploadApproval != nil {
		return invalid("plan_stale", "the original upload still requires approval after retry; inspect the upload and deployment before creating a new plan")
	}
	info := apiErr.Problem.AgentApproval
	if info == nil || validateSegment(info.ID, "approval_id") != nil || info.ExpiresAt == "" {
		return applyErr
	}
	got, err := m.client.GetAgentApproval(ctx, info.ID)
	if err != nil {
		return err
	}
	uploadID := approvalUploadID(got)
	if uploadID == "" {
		// Other writes in deploy can ask for approval too. They keep their
		// existing retry contract and never extend this upload plan.
		return applyErr
	}
	deadline, parseErr := time.Parse(time.RFC3339, got.ExpiresAt)
	now := time.Now()
	if parseErr != nil || !deadline.After(now) || deadline.After(now.Add(api.AgentApprovalTTL)) ||
		got.ID != info.ID || got.Category != info.Category || got.ExpiresAt != info.ExpiresAt ||
		(got.Status != api.AgentApprovalPending && got.Status != api.AgentApprovalApproved) {
		return invalid("plan_stale", "the upload approval changed or expired before the bridge could retain the plan")
	}
	upload, err := m.client.GetUploadSession(ctx, uploadID)
	if err != nil {
		return err
	}
	if !completeOpenUpload(upload, uploadID, p.App) {
		return invalid("plan_stale", "the approval does not identify a complete open upload for the reviewed app")
	}
	appID, err := m.checkUploadApprovalBaseline(ctx, p)
	if err != nil {
		return err
	}
	next := *p
	next.AppID, next.ExpiresAt = appID, deadline
	next.uploadApproval = &pendingUploadApproval{uploadID: uploadID, info: *info}
	if err := m.opts.uploads.retainApproval(&next, m.opts.planNamespace); err != nil {
		return err
	}
	*p = next
	return applyErr
}

func approvalUploadID(a api.AgentApprovalResponse) string {
	if a.Method != http.MethodPost || !a.BodyBound {
		return ""
	}
	path, ok := strings.CutPrefix(a.Path, "/v1/uploads/")
	id, commit := strings.CutSuffix(path, "/commit")
	if !ok || !commit || validateSegment(id, "upload_id") != nil {
		return ""
	}
	return id
}

func completeOpenUpload(upload api.UploadSessionResponse, id, app string) bool {
	return upload.UploadID == id && upload.AppSlug == app && upload.Status == "open" &&
		upload.DeploymentID == nil && upload.TotalSize > 0 && upload.ReceivedBytes == upload.TotalSize
}

// checkUploadApproval runs before an approval retry. A used approval can
// recover its exact accepted release without running the CLI again.
func (m *management) checkUploadApproval(ctx context.Context, p *releasePlan) (any, error) {
	a := p.uploadApproval
	got, err := m.client.GetAgentApproval(ctx, a.info.ID)
	if err != nil {
		return nil, err
	}
	if got.ID != a.info.ID || approvalUploadID(got) != a.uploadID || got.Category != a.info.Category || got.ExpiresAt != a.info.ExpiresAt {
		return nil, invalid("plan_stale", "the original approval no longer matches this plan's upload")
	}
	switch got.Status {
	case api.AgentApprovalPending:
		return nil, a.problem(http.StatusPreconditionRequired, api.CodeApprovalRequired, "Approval required", "wait for the original upload approval, then retry the same plan", got)
	case api.AgentApprovalDenied:
		return nil, a.problem(http.StatusForbidden, api.CodeApprovalDenied, "Approval denied", "the original upload approval was denied", got)
	case api.AgentApprovalExpired:
		return nil, a.problem(http.StatusGone, api.CodeApprovalExpired, "Approval expired", "the original upload approval expired; create a new plan", got)
	case api.AgentApprovalApproved, api.AgentApprovalConsumed:
	default:
		return nil, invalid("invalid_response", "the original upload approval returned an unknown status")
	}
	upload, err := m.client.GetUploadSession(ctx, a.uploadID)
	if err != nil {
		return nil, err
	}
	if got.Status == api.AgentApprovalConsumed {
		if upload.UploadID != a.uploadID || upload.AppSlug != p.App || upload.Status != "committed" ||
			upload.DeploymentID == nil || validateSegment(*upload.DeploymentID, "deployment_id") != nil {
			return nil, invalid("upload_outcome_unknown", "the original approval was used; inspect the app's deployments for its outcome")
		}
		return m.localDeployResult(ctx, p, localDeployReceipt{ID: *upload.DeploymentID})
	}
	if !completeOpenUpload(upload, a.uploadID, p.App) {
		return nil, invalid("plan_stale", "the approved upload is no longer open with the reviewed source; inspect the app's deployments before creating a new plan")
	}
	_, err = m.checkUploadApprovalBaseline(ctx, p)
	return nil, err
}

func (a *pendingUploadApproval) problem(status int, code, title, detail string, got api.AgentApprovalResponse) error {
	problem := api.NewProblem(status, code, title, detail)
	info := a.info
	info.Status, info.Note = got.Status, got.Note
	problem.AgentApproval = &info
	return &api.APIError{Problem: *problem}
}

// The first attempt may have created the app, but the approval prevented the
// upload commit. Pin that app identity and the reviewed release state.
func (m *management) checkUploadApprovalBaseline(ctx context.Context, p *releasePlan) (string, error) {
	app, err := m.client.GetApp(ctx, p.App)
	if notFound(err) {
		return "", invalid("plan_stale", "the original app is gone; create a new plan")
	}
	if err != nil {
		return "", err
	}
	if app.ID == "" || app.Slug != p.App || (p.AppID != "" && app.ID != p.AppID) || !m.appIDAllowed(app.ID) {
		return "", invalid("plan_stale", "the reviewed app identity changed; create a new plan")
	}
	if p.Action == "deploy" {
		return app.ID, m.verifyBaseline(ctx, p)
	}
	health, err := m.scopedHealth(ctx, appArgs{App: p.App})
	if err != nil {
		return "", err
	}
	if health.AppID != app.ID || len(health.ServingDeploymentIDs) != 0 || health.LatestDeploymentID != p.latestID {
		return "", invalid("plan_stale", "the app's release state changed while waiting for approval; create a new plan")
	}
	return app.ID, nil
}
