package state

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// Account-family credential columns (ADR 20261029202403). Each names the
// column its keyed digests are bound to.
const (
	credTagAPIKey                  credhash.Tag = "api_keys.key_sha256"
	credTagOIDCToken               credhash.Tag = "oidc_exchanged_tokens.token_hash"
	credTagDeployToken             credhash.Tag = "deploy_tokens.token_sha256"
	credTagTenantAccessToken       credhash.Tag = "platform_tenant_access_tokens.token_hash"
	credTagHostingAutomation       credhash.Tag = "hosting_automation_credentials.token_hash"
	credTagCLIAuthCode             credhash.Tag = "cli_auth_codes.token_hash"
	credTagCLIDeviceCode           credhash.Tag = "cli_auth_codes.device_hash"
	credTagLoginToken              credhash.Tag = "login_tokens.token_hash"
	credTagMFADisableRequest       credhash.Tag = "mfa_disable_requests.token_hash"
	credTagOrgInvitation           credhash.Tag = "org_invitations.token_hash"
	credTagPromotionPlanToken      credhash.Tag = "project_environment_approvals.plan_token_hash"
	credTagPromotionApprovalToken  credhash.Tag = "project_environment_approvals.approval_token_hash"
	credTagEmailVerification       credhash.Tag = "email_verification_tokens.token_hash"
	credTagIssueIngestToken        credhash.Tag = "issue_ingest_tokens.token_hash"
	credTagInboundWebhook          credhash.Tag = "inbound_webhook_endpoints.token_hash"
	credTagArtifactGrant           credhash.Tag = "execution_artifact_grants.token_hash"
	credTagRealtimePublisher       credhash.Tag = "managed_realtime_publisher_credentials.token_hash"
	credTagRealtimeWorker          credhash.Tag = "managed_realtime_worker_credentials.token_hash"
	credTagRealtimeTicket          credhash.Tag = "managed_realtime_subscription_tickets.token_hash"
	credTagQueueLeaseReceipt       credhash.Tag = "invocation_queue_lease_receipts.token_hash"
	credTagEnvironmentQueueReceipt credhash.Tag = "invocation_environment_queue_receipts.token_hash"
	credTagPublishReceipt          credhash.Tag = "workflow_automation_publish_receipts.token_hash"
	credTagConsumerSignupClaim     credhash.Tag = "api_consumer_signups.claim_hash"

	// Account-family columns outside pkg/state, keyed by their packages
	// through StoredCredential and CredentialMatches.
	CredentialColumnMagicLink          credhash.Tag = "magic_link_tokens.token_hash"
	CredentialColumnPasswordReset      credhash.Tag = "password_reset_tokens.token_hash"
	CredentialColumnMFADisable         credhash.Tag = "mfa_disable_tokens.token_hash"
	CredentialColumnSlackOAuthState    credhash.Tag = "issue_slack_oauth_flows.state_hash"
	CredentialColumnLinearOAuthState   credhash.Tag = "issue_tracker_linear_oauth_flows.state_hash"
	CredentialColumnEmailUnsubscribe   credhash.Tag = "product_control_email_deliveries.unsubscribe_hash"
	CredentialColumnVerificationOutbox credhash.Tag = "platform_mail_producer_outbox.verification_token_hash"

	// CredentialColumnConsumerKey and CredentialColumnForkToken are checked
	// by gatewayd-internal, which loads the row itself and asks apid whether
	// the presented token matches (MatchCredential).
	CredentialColumnConsumerKey credhash.Tag = "consumer_keys.hashed_secret"
	CredentialColumnForkToken   credhash.Tag = "app_forks.access_token_hash"
)

// GatewayCheckedCredential reports whether a gateway may ask apid to match
// a value of column.
func GatewayCheckedCredential(column string) (credhash.Tag, bool) {
	switch tag := credhash.Tag(column); tag {
	case CredentialColumnConsumerKey, CredentialColumnForkToken:
		return tag, true
	}
	return "", false
}

// CredentialMatches reports whether stored, read from column tag, is the
// value a credential whose token hashes to sum may hold under this
// process's account policy.
func CredentialMatches(tag credhash.Tag, sum, stored []byte) bool {
	return credhash.Account().Matches(tag, sum, stored)
}

// StoredCredential is the value to write in column tag for a credential
// whose token hashes to sum.
func StoredCredential(tag credhash.Tag, sum []byte) []byte {
	return storedCredential(tag, sum)
}

// credentialLookups are the stored values to try for a credential: a stored
// digest (credhash.DigestSize bytes, as a caller re-checking a stored key
// passes) is looked up as it is; a token's SHA-256 gives the account
// policy's candidates, the current key's digest first.
func credentialLookups(tag credhash.Tag, sum []byte) [][]byte {
	if credhash.Keyed(sum) {
		return [][]byte{sum}
	}
	return credhash.Account().Lookups(tag, sum)
}

// storedCredential is the value to write for a credential whose token
// hashes to sum: a keyed digest once keyed writes are on, the plain hash
// before. A stored digest is written as it is.
func storedCredential(tag credhash.Tag, sum []byte) []byte {
	if credhash.Keyed(sum) {
		return sum
	}
	return credhash.Account().Store(tag, sum)
}

// staleCredential reports whether a credential found by its token's SHA-256
// should be rewritten under the current key, and the value to write.
func staleCredential(tag credhash.Tag, sum, stored []byte) ([]byte, bool) {
	p := credhash.Account()
	if len(sum) != credhash.PlainSize || !p.Stale(stored) {
		return nil, false
	}
	return p.Store(tag, sum), true
}

// firstCredential runs find for each lookup in turn and returns the first
// result that is not ErrNotFound.
func firstCredential[T any](ctx context.Context, lookups [][]byte, find func(context.Context, []byte) (T, error)) (T, error) {
	var zero T
	for _, value := range lookups {
		out, err := find(ctx, value)
		if err == nil || !errors.Is(err, ErrNotFound) {
			return out, err
		}
	}
	return zero, ErrNotFound
}

// credentialEntry finds a MemStore row keyed by the hex of its stored
// value, trying each lookup in turn, and returns the map key it found.
func credentialEntry[T any](rows map[string]T, tag credhash.Tag, sum []byte) (T, string, bool) {
	for _, value := range credentialLookups(tag, sum) {
		key := hex.EncodeToString(value)
		if row, ok := rows[key]; ok {
			return row, key, true
		}
	}
	var zero T
	return zero, "", false
}

// rawCredentialKey finds the MemStore map key (the stored value's raw
// bytes) a credential is stored under, trying each lookup in turn.
func rawCredentialKey[T any](rows map[string]T, tag credhash.Tag, sum []byte) (string, bool) {
	for _, value := range credentialLookups(tag, sum) {
		if _, ok := rows[string(value)]; ok {
			return string(value), true
		}
	}
	return "", false
}

// credentialIn reports whether stored is one of sum's lookups: a stored
// digest compared as it is, or a token's SHA-256 under the account policy.
func credentialIn(tag credhash.Tag, sum, stored []byte) bool {
	for _, value := range credentialLookups(tag, sum) {
		if string(value) == string(stored) {
			return true
		}
	}
	return false
}
