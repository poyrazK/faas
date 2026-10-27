package issuetrackers

import (
	"context"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ConfigureLinearConnection returns public metadata; it never exposes ciphertext.
func ConfigureLinearConnection(ctx context.Context, store state.IssueTrackerLinearStore, verifier LinearConnectionVerifier, recipients []*age.X25519Recipient, accountID, appID string, in api.UpdateIssueTrackerLinearConnectionRequest) (api.IssueTrackerLinearConnection, error) {
	if in.ExpectedRevision < 0 || !in.Connected && (in.APIKey != "" || in.TeamID != "" || in.WebhookSecret != "") {
		return api.IssueTrackerLinearConnection{}, api.ErrValidation("expected_revision must be nonnegative; disconnected requests must omit credentials and team")
	}
	record := state.IssueTrackerLinearRecord{AccountID: accountID, AppID: appID, Connection: api.IssueTrackerLinearConnection{Connected: in.Connected}}
	if in.Connected {
		if len(recipients) == 0 {
			return record.Connection, api.ErrCapacity("Linear credential sealing is unavailable")
		}
		scope, sealed, err := SealLinearCredentialTo(ctx, verifier, recipients, accountID, appID, in.TeamID, in.APIKey, in.WebhookSecret)
		if err != nil {
			return record.Connection, api.NewProblem(400, "issue_tracker_linear_verification_failed", "Linear connection not verified", "check the API key and active team UUID")
		}
		record.Connection.WebhookEnabled = in.WebhookSecret != ""
		record.Connection.WorkspaceID = scope.Identity.WorkspaceID
		record.Connection.TeamID = scope.Identity.TeamID
		record.Connection.ViewerID = scope.Identity.ViewerID
		record.SealedCredential = sealed
	}
	out, err := store.ConfigureIssueTrackerLinearConnection(ctx, record, in.ExpectedRevision)
	return out.Connection, err
}
