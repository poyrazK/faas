package state

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

// Account-family credential columns (ADR 20261029202403). Each names the
// column its keyed digests are bound to.
const (
	credTagAPIKey                       credhash.Tag = "api_keys.key_sha256"
	credTagOIDCToken                    credhash.Tag = "oidc_exchanged_tokens.token_hash"
	credTagDeployToken                  credhash.Tag = "deploy_tokens.token_sha256"
	credTagTenantAccessToken            credhash.Tag = "platform_tenant_access_tokens.token_hash"
	credTagHostingAutomation            credhash.Tag = "hosting_automation_credentials.token_hash"
	credTagCLIAuthCode                  credhash.Tag = "cli_auth_codes.token_hash"
	credTagCLIDeviceCode                credhash.Tag = "cli_auth_codes.device_hash"
	credTagLoginToken                   credhash.Tag = "login_tokens.token_hash"
	credTagMFADisableRequest            credhash.Tag = "mfa_disable_requests.token_hash"
	credTagOrgInvitation                credhash.Tag = "org_invitations.token_hash"
	credTagPromotionPlanToken           credhash.Tag = "project_environment_approvals.plan_token_hash"
	credTagPromotionApprovalToken       credhash.Tag = "project_environment_approvals.approval_token_hash"
	credTagEmailVerification            credhash.Tag = "email_verification_tokens.token_hash"
	credTagIssueIngestToken             credhash.Tag = "issue_ingest_tokens.token_hash"
	credTagInboundWebhook               credhash.Tag = "inbound_webhook_endpoints.token_hash"
	credTagArtifactGrant                credhash.Tag = "execution_artifact_grants.token_hash"
	credTagRealtimePublisher            credhash.Tag = "managed_realtime_publisher_credentials.token_hash"
	credTagRealtimeWorker               credhash.Tag = "managed_realtime_worker_credentials.token_hash"
	credTagRealtimeTicket               credhash.Tag = "managed_realtime_subscription_tickets.token_hash"
	credTagQueueLeaseReceipt            credhash.Tag = "invocation_queue_lease_receipts.token_hash"
	credTagEnvironmentQueueReceipt      credhash.Tag = "invocation_environment_queue_receipts.token_hash"
	credTagPublishReceipt               credhash.Tag = "workflow_automation_publish_receipts.token_hash"
	CredentialColumnConsumerSignupClaim credhash.Tag = "api_consumer_signups.claim_hash"

	// Account-family columns outside pkg/state, keyed by their packages
	// through StoredCredential and CredentialMatches.
	CredentialColumnMagicLink        credhash.Tag = "magic_link_tokens.token_hash"
	CredentialColumnPasswordReset    credhash.Tag = "password_reset_tokens.token_hash"
	CredentialColumnMFADisable       credhash.Tag = "mfa_disable_tokens.token_hash"
	CredentialColumnSlackOAuthState  credhash.Tag = "issue_slack_oauth_flows.state_hash"
	CredentialColumnLinearOAuthState credhash.Tag = "issue_tracker_linear_oauth_flows.state_hash"
	CredentialColumnEmailUnsubscribe credhash.Tag = "product_control_email_deliveries.unsubscribe_hash"

	// CredentialColumnConsumerKey and CredentialColumnForkToken are checked
	// by gatewayd-internal, which loads the row itself and asks apid whether
	// the presented token matches (MatchCredential).
	CredentialColumnConsumerKey credhash.Tag = "consumer_keys.hashed_secret"
	CredentialColumnForkToken   credhash.Tag = "app_forks.access_token_hash"
)

// Columns of the other families that pkg/state writes or looks up (ADR
// 20261029202403 amendment 1). The families' readers outside pkg/state use
// the same names.
const (
	credTagOutboundAIAccessToken credhash.Tag = "outbound_ai_access_tokens.token_hash"
	credTagUploadGrant           credhash.Tag = "object_storage_upload_grants.token_hash"
	credTagInteractiveAttach     credhash.Tag = api.AttachTokenCredentialTag
)

// Columns of other families that a daemon holding no ring for them asks
// apid to match (ADR 20261029202403 amendment 1, decision 2).
const (
	// CredentialColumnOperationCapability is checked by gatewayd-internal
	// when it admits a platform-tenant operation invocation.
	CredentialColumnOperationCapability credhash.Tag = "customer_operations.execution_capability_digest"
	// CredentialColumnDevBridgeAttachment and CredentialColumnDevBridgeRequest
	// are checked by gatewayd-internal when it relays dev-bridge traffic.
	CredentialColumnDevBridgeAttachment = devbridge.AttachmentDigestColumn
	CredentialColumnDevBridgeRequest    = devbridge.RequestDigestColumn
	// CredentialColumnDataAPIServerKey is checked by realtimed when a Data
	// API server key opens a live query.
	CredentialColumnDataAPIServerKey credhash.Tag = api.DataAPIServerKeyCredentialTag
)

// GatewayCheckedCredential reports whether a gateway may ask apid to match
// a value of column, and the family whose policy answers.
func GatewayCheckedCredential(column string) (credhash.Tag, credhash.Family, bool) {
	switch tag := credhash.Tag(column); tag {
	case CredentialColumnConsumerKey, CredentialColumnForkToken:
		return tag, credhash.FamilyAccount, true
	case CredentialColumnOperationCapability:
		return tag, credhash.FamilyScheduler, true
	case CredentialColumnDevBridgeAttachment, CredentialColumnDevBridgeRequest:
		return tag, credhash.FamilyDevBridge, true
	case CredentialColumnDataAPIServerKey:
		return tag, credhash.FamilyDataAPI, true
	}
	return "", "", false
}

// FamilyCredentialMatches reports whether stored, read from column tag, is
// the value family's credential whose token hashes to sum may hold, under
// this process's policy for family or the remote installed for it
// (credhash.Match).
func FamilyCredentialMatches(ctx context.Context, family credhash.Family, tag credhash.Tag, sum, stored []byte) bool {
	return credhash.Match(ctx, family, tag, sum, stored)
}

// familyStoredCredential is the value to write in column tag for family's
// credential whose token hashes to sum. A stored digest is written as it is.
func familyStoredCredential(family credhash.Family, tag credhash.Tag, sum []byte) []byte {
	if credhash.Keyed(sum) {
		return sum
	}
	return credhash.For(family).Store(tag, sum)
}

// familyStoredCredentialHex is familyStoredCredential for a column that
// holds hex text. A value that is not a hex SHA-256 is written as it is.
func familyStoredCredentialHex(family credhash.Family, tag credhash.Tag, sumHex string) string {
	sum, err := hex.DecodeString(sumHex)
	if err != nil || len(sum) != credhash.PlainSize {
		return sumHex
	}
	return hex.EncodeToString(familyStoredCredential(family, tag, sum))
}

// familyCredentialLookups are the stored values to try for family's
// credential whose token hashes to sum; a stored digest is looked up as it
// is.
func familyCredentialLookups(family credhash.Family, tag credhash.Tag, sum []byte) [][]byte {
	if credhash.Keyed(sum) {
		return [][]byte{sum}
	}
	return credhash.For(family).Lookups(tag, sum)
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

// StoredCredentialHex is the hex value to write in column tag for a
// credential whose hex SHA-256 is sumHex.
func StoredCredentialHex(tag credhash.Tag, sumHex string) string {
	return storedCredentialHex(tag, sumHex)
}

// FirstCredentialHex runs find with each hex value a credential whose hex
// SHA-256 is sumHex may be stored as in column tag, the current key's
// digest first, and returns the first result that is not ErrNotFound.
func FirstCredentialHex[T any](ctx context.Context, tag credhash.Tag, sumHex string, find func(context.Context, string) (T, error)) (T, error) {
	lookups := []string{sumHex}
	if sum, err := hex.DecodeString(sumHex); err == nil && len(sum) == credhash.PlainSize {
		lookups = lookups[:0]
		for _, value := range credentialLookups(tag, sum) {
			lookups = append(lookups, hex.EncodeToString(value))
		}
	}
	var zero T
	for _, value := range lookups {
		out, err := find(ctx, value)
		if err == nil || !errors.Is(err, ErrNotFound) {
			return out, err
		}
	}
	return zero, ErrNotFound
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

// storedCredentialShape reports whether b has the length of a stored
// credential: a plain SHA-256 or a keyed digest.
func storedCredentialShape(b []byte) bool {
	return len(b) == credhash.PlainSize || credhash.Keyed(b)
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

// firstCredentialMatch is firstCredential that also returns the stored value
// that matched, for a caller that rewrites it under the current key.
func firstCredentialMatch[T any](ctx context.Context, lookups [][]byte, find func(context.Context, []byte) (T, error)) (T, []byte, error) {
	var zero T
	for _, value := range lookups {
		out, err := find(ctx, value)
		if err == nil {
			return out, value, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return zero, nil, err
		}
	}
	return zero, nil, ErrNotFound
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

// approvalTokenPairs are the (plan, approval) stored hex pairs to try for a
// promotion approval's token hashes (ADR 20261029202403): both columns of a
// row are written in one insert under one key, so the pairs run key by key,
// the current key's first, then the plain pair while plain lookup is on.
// Hashes that are not a token's hex SHA-256 are tried as given.
func approvalTokenPairs(planHex, approvalHex string) [][2]string {
	plan, perr := hex.DecodeString(planHex)
	approval, aerr := hex.DecodeString(approvalHex)
	if perr != nil || aerr != nil || len(plan) != credhash.PlainSize || len(approval) != credhash.PlainSize {
		return [][2]string{{planHex, approvalHex}}
	}
	plans := credentialLookups(credTagPromotionPlanToken, plan)
	approvals := credentialLookups(credTagPromotionApprovalToken, approval)
	out := make([][2]string, 0, len(plans))
	for i := 0; i < len(plans) && i < len(approvals); i++ {
		out = append(out, [2]string{hex.EncodeToString(plans[i]), hex.EncodeToString(approvals[i])})
	}
	return out
}

// storedApprovalTokenHash is the hex value to write for a promotion
// approval token hash.
func storedApprovalTokenHash(tag credhash.Tag, sumHex string) string {
	sum, err := hex.DecodeString(sumHex)
	if err != nil || len(sum) != credhash.PlainSize {
		return sumHex
	}
	return hex.EncodeToString(storedCredential(tag, sum))
}

// approvalTokensMatch reports whether a stored approval holds one of the
// token-hash pairs approvalTokenPairs gives.
func approvalTokensMatch(approval ProjectEnvironmentApproval, planHex, approvalHex string) bool {
	for _, pair := range approvalTokenPairs(planHex, approvalHex) {
		if approval.PlanTokenHash == pair[0] && approval.ApprovalTokenHash == pair[1] {
			return true
		}
	}
	return false
}

// storedCredentialHex is storedCredential for a column that holds hex
// text. A value that is not a hex SHA-256 is written as it is.
func storedCredentialHex(tag credhash.Tag, sumHex string) string {
	sum, err := hex.DecodeString(sumHex)
	if err != nil || len(sum) != credhash.PlainSize {
		return sumHex
	}
	return hex.EncodeToString(storedCredential(tag, sum))
}

// credentialHexMatches reports, in constant time per candidate, whether
// storedHex is the hex value a credential whose hex SHA-256 is sumHex may
// hold.
func credentialHexMatches(tag credhash.Tag, sumHex, storedHex string) bool {
	sum, err := hex.DecodeString(sumHex)
	stored, serr := hex.DecodeString(storedHex)
	if err != nil || serr != nil {
		return false
	}
	return CredentialMatches(tag, sum, stored)
}
