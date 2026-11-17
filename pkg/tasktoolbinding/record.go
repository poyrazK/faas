// Package tasktoolbinding models owner-selected, read-only hosted Task probes.
package tasktoolbinding

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var ErrInvalid = errors.New("invalid hosted Task binding qualification")
var ErrConflict = errors.New("hosted Task binding qualification conflict")
var namespacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]*$`)
var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Policy is derived by the native handler from the exact encrypted, reviewed
// source configuration. It is never decoded from an API request.
type Policy struct {
	WebDatabaseEnv, WorkerDatabaseEnv   string
	WebOwnerEnv, WorkerOwnerEnv         string
	WebNamespaceEnv, WorkerNamespaceEnv string
	WebPayloadEnv, WorkerPayloadEnv     string
	OwnerKeyID                          string
	PayloadVersion                      int
	PreviousKeys                        []PreviousKey
}
type PreviousKey struct{ ID, WebEnv, WorkerEnv string }

// Pin records opaque native versions, not plaintext or encrypted credentials.
type Pin struct {
	Role, AppID, Key                     string
	SecretVersion                        int64
	CipherSHA256                         string
	BindingID, DatabaseID, CredentialRef string
	Generation                           int64
	Access                               string
}
type Record struct {
	Public     api.HostedTaskBindingVerification
	Selection  api.HostedTaskBindingSelection
	Policy     Policy
	Pins       []Pin
	OwnerKeyID string
	Claim      string
}

func validEnv(v string) bool { return api.ValidateEnvKey(v) == nil }
func ValidateSelection(s api.HostedTaskBindingSelection) error {
	if !toolpublication.ValidID(s.IntentSHA256) || !toolpublication.ValidID(s.ReviewSHA256) || len(s.Namespace) > api.HostedTaskBindingNamespaceMaxBytes || !namespacePattern.MatchString(s.Namespace) {
		return ErrInvalid
	}
	for _, ref := range []string{s.WebDatabaseRef, s.WorkerDatabaseRef, s.MigrationDatabaseRef, s.ObserverDatabaseRef} {
		if !strings.HasPrefix(ref, api.SecretRefPrefix) || !validEnv(strings.TrimPrefix(ref, api.SecretRefPrefix)) {
			return ErrInvalid
		}
	}
	if s.WorkerDatabaseRef == s.MigrationDatabaseRef {
		return ErrInvalid
	}
	return nil
}
func (p Policy) Valid() bool {
	for _, v := range []string{p.WebDatabaseEnv, p.WorkerDatabaseEnv, p.WebOwnerEnv, p.WorkerOwnerEnv, p.WebNamespaceEnv, p.WorkerNamespaceEnv} {
		if !validEnv(v) {
			return false
		}
	}
	if !validKeyID(p.OwnerKeyID) || p.PayloadVersion < 0 || p.PayloadVersion > 2 || len(p.PreviousKeys) > api.HostedTaskBindingPreviousKeysMax {
		return false
	}
	if (p.WebPayloadEnv == "") != (p.WorkerPayloadEnv == "") {
		return false
	}
	if p.WebPayloadEnv != "" && (!validEnv(p.WebPayloadEnv) || !validEnv(p.WorkerPayloadEnv)) {
		return false
	}
	seen := map[string]bool{p.OwnerKeyID: true}
	for _, k := range p.PreviousKeys {
		if !validKeyID(k.ID) || seen[k.ID] || !validEnv(k.WebEnv) || !validEnv(k.WorkerEnv) {
			return false
		}
		seen[k.ID] = true
	}
	return true
}
func validKeyID(id string) bool {
	return len(id) <= api.HostedTaskBindingKeyIDMaxBytes && keyIDPattern.MatchString(id)
}
func Fingerprint(r Record) string {
	b, _ := json.Marshal(struct {
		Domain                      string
		Account, ID, Intent, Review string
		Selection                   api.HostedTaskBindingSelection
		Policy                      Policy
		Pins                        []Pin
		Owner                       string
		Created, Expires            time.Time
	}{"hosted-task-binding:v1", r.Public.AccountID, r.Public.IntentID, r.Public.IntentSHA256, r.Public.ReviewSHA256, r.Selection, r.Policy, r.Pins, r.OwnerKeyID, r.Public.CreatedAt, r.Public.ExpiresAt})
	return toolpublication.Digest(b)
}
func New(source api.HostedTaskSourceIntent, selection api.HostedTaskBindingSelection, policy Policy, pins []Pin, owner string, now time.Time) (Record, error) {
	var out Record
	if uuid.Validate(source.AccountID) != nil || !toolpublication.ValidID(source.ID) || ValidateSelection(selection) != nil || !policy.Valid() || source.ReviewStatus != "approved" || source.SourceState != "retained" || source.IntentSHA256 != selection.IntentSHA256 || source.ReviewSHA256 != selection.ReviewSHA256 || !now.Before(source.ExpiresAt) || len(pins) < 6 || len(pins) > api.HostedTaskBindingMaxPins || owner != "" && uuid.Validate(owner) != nil {
		return out, ErrInvalid
	}
	now = now.UTC().Truncate(time.Microsecond)
	policy.PreviousKeys = append([]PreviousKey(nil), policy.PreviousKeys...)
	out = Record{Public: api.HostedTaskBindingVerification{Version: 1, AccountID: source.AccountID, IntentID: source.ID, IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Status: "selected", Checks: Results("not_checked"), CreatedAt: now, ExpiresAt: source.ExpiresAt, OwnerAuthorizationRequired: true}, Selection: selection, Policy: policy, Pins: append([]Pin(nil), pins...), OwnerKeyID: owner}
	out.Public.BindingSHA256 = Fingerprint(out)
	if err := ValidateRecord(out); err != nil {
		return Record{}, err
	}
	return out, nil
}

func ResultNames() []string {
	return []string{"namespace", "owner_keys", "configured_payload_keys", "database_schema", "runtime_roles", "migration_owner", "observer_read_only"}
}
func Results(status string) []api.HostedTaskBindingResult {
	var out []api.HostedTaskBindingResult
	for _, n := range ResultNames() {
		out = append(out, api.HostedTaskBindingResult{Name: n, Status: status})
	}
	return out
}
func ValidResults(results []api.HostedTaskBindingResult) bool {
	names := ResultNames()
	if len(results) != len(names) {
		return false
	}
	for i, r := range results {
		if r.Name != names[i] || r.Status != "passed" && r.Status != "failed" && r.Status != "not_checked" {
			return false
		}
	}
	return true
}
func Passed(results []api.HostedTaskBindingResult) bool {
	if !ValidResults(results) {
		return false
	}
	for _, r := range results {
		if r.Status != "passed" {
			return false
		}
	}
	return true
}
func View(p api.HostedTaskBindingVerification, now time.Time) api.HostedTaskBindingVerification {
	p.ReadyToApply, p.BindingsVerified, p.OwnerAuthorizationRequired = false, false, true
	if p.Status != "revoked" && (!now.Before(p.ExpiresAt) || p.Status == "passed" && (p.ValidUntil == nil || !now.Before(*p.ValidUntil))) {
		p.Status = "expired"
	}
	return p
}
func ValidateReceipt(p api.HostedTaskBindingVerification, account, id string) error {
	if p.Version != 1 || p.AccountID != account || uuid.Validate(account) != nil || p.IntentID != id || !toolpublication.ValidID(id) || !toolpublication.ValidID(p.IntentSHA256) || !toolpublication.ValidID(p.ReviewSHA256) || !toolpublication.ValidID(p.BindingSHA256) || p.ReadyToApply || p.BindingsVerified || !p.OwnerAuthorizationRequired || !ValidResults(p.Checks) || p.CreatedAt.IsZero() || !p.CreatedAt.Before(p.ExpiresAt) || p.ExpiresAt.Sub(p.CreatedAt) > api.HostedTaskSourceRetention {
		return ErrInvalid
	}
	switch p.Status {
	case "selected", "verifying", "passed", "failed", "stale", "revoked", "expired":
	default:
		return ErrInvalid
	}
	if (p.Status == "selected" || p.Status == "verifying") && (p.CheckedAt != nil || p.ValidUntil != nil) {
		return ErrInvalid
	}
	if p.CheckedAt != nil && (p.CheckedAt.Before(p.CreatedAt) || !p.CheckedAt.Before(p.ExpiresAt)) {
		return ErrInvalid
	}
	if p.ValidUntil != nil && (p.CheckedAt == nil || !p.CheckedAt.Before(*p.ValidUntil) || p.ValidUntil.After(p.ExpiresAt) || p.ValidUntil.Sub(*p.CheckedAt) > api.HostedTaskBindingEvidenceTTL) {
		return ErrInvalid
	}
	if p.Status == "passed" && (p.CheckedAt == nil || p.ValidUntil == nil || !Passed(p.Checks)) {
		return ErrInvalid
	}
	if p.Status == "failed" && p.CheckedAt == nil {
		return ErrInvalid
	}
	return nil
}

func ValidateRecord(r Record) error {
	if ValidateReceipt(r.Public, r.Public.AccountID, r.Public.IntentID) != nil || ValidateSelection(r.Selection) != nil || !r.Policy.Valid() || r.Public.IntentSHA256 != r.Selection.IntentSHA256 || r.Public.ReviewSHA256 != r.Selection.ReviewSHA256 || Fingerprint(r) != r.Public.BindingSHA256 || len(r.Pins) < 6 || len(r.Pins) > api.HostedTaskBindingMaxPins || r.OwnerKeyID != "" && uuid.Validate(r.OwnerKeyID) != nil {
		return ErrInvalid
	}
	if (r.Public.Status == "verifying" || r.Public.CheckedAt != nil) && uuid.Validate(r.Claim) != nil {
		return ErrInvalid
	}
	for _, p := range r.Pins {
		if p.Role == "" || uuid.Validate(p.AppID) != nil || !validEnv(p.Key) || p.SecretVersion < 0 || !toolpublication.ValidID(p.CipherSHA256) {
			return ErrInvalid
		}
	}
	return nil
}

// Mutate is the one-claim transition used under native journal locks. A lost
// response, expired claim or restarted process never causes a second probe.
func Mutate(r Record, action, digest, claim string, checks []api.HostedTaskBindingResult, now time.Time) (Record, error) {
	r.Public = View(r.Public, now)
	if action == "get" {
		return r, nil
	}
	if digest != r.Public.BindingSHA256 {
		return r, ErrConflict
	}
	switch action {
	case "revoke":
		r.Public.Status = "revoked"
	case "claim":
		if r.Public.Status != "selected" {
			return r, nil
		}
		if uuid.Validate(claim) != nil {
			return r, ErrInvalid
		}
		r.Claim = claim
		r.Public.Status = "verifying"
	case "record":
		if r.Claim != claim || uuid.Validate(claim) != nil {
			return r, ErrConflict
		}
		if r.Public.Status != "verifying" {
			return r, nil
		}
		if !ValidResults(checks) {
			return r, ErrInvalid
		}
		now = now.UTC().Truncate(time.Microsecond)
		r.Public.CheckedAt = &now
		r.Public.Checks = append([]api.HostedTaskBindingResult(nil), checks...)
		r.Public.Status = "failed"
		if Passed(checks) {
			until := now.Add(api.HostedTaskBindingEvidenceTTL)
			if until.After(r.Public.ExpiresAt) {
				until = r.Public.ExpiresAt
			}
			r.Public.ValidUntil = &until
			r.Public.Status = "passed"
		}
	default:
		return r, ErrInvalid
	}
	return r, ValidateRecord(r)
}
