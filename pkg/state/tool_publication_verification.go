package state

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// This optional store serializes owner decisions and each credential dispatch
// with revocation/rotation. A dispatched HTTP request may finish after revoke.
type ToolPublicationVerificationStore interface {
	ChangeToolPublicationVerification(context.Context, string, string, string, string, ToolPublicationVerificationChange) (ToolPublicationVerificationResult, error)
}

type ToolPublicationVerificationChange struct {
	Review   *api.ToolPublicationVerification
	Decision *api.ToolPublicationVerificationDecision
	Outcome  *api.ToolPublicationVerificationOutcome
}

type ToolPublicationVerificationResult struct {
	Verification api.ToolPublicationVerification
	Created      bool
	Secret       *AppSecret // internal dispatch only; never serialize this result
}

type toolPublicationTestCredential struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

// Private approval metadata shares the existing bounded publication payload.
// Public reads decode only ToolPublicationRecord and cannot expose this field.
type storedToolPublication struct {
	api.ToolPublicationRecord
	TestCredential *toolPublicationTestCredential `json:"verification_credential,omitempty"`
}

func changeToolVerification(p *storedToolPublication, key, action string, in ToolPublicationVerificationChange, secret *AppSecret) (ToolPublicationVerificationResult, error) {
	var out ToolPublicationVerificationResult
	if !p.Intent.PlatformVerification || p.Intent.AccessMode != "external-oauth" {
		return out, ErrInvalidArgument
	}
	if action == "review" && p.Verification == nil {
		if p.Status != "running" || !time.Now().Before(p.Deadline) {
			return out, ErrConflict
		}
		v := in.Review
		if v == nil || v.PublicationID != p.ID || v.KeyID != key || !toolpublication.ValidID(v.DeploymentSourceSHA256) || !toolpublication.ValidID(v.CatalogSHA256) || v.RequestSHA256 != toolpublication.VerificationDigest(*v, p.Intent) {
			return out, ErrInvalidArgument
		}
		v.Status, v.CreatedAt, v.Deadline, v.Outcome = "pending", time.Now().UTC(), p.Deadline, nil
		p.Verification = v
	}
	if p.Verification == nil {
		return out, ErrNotFound
	}
	v := toolpublication.VerificationView(*p.Verification, time.Now())
	if action != "approve" && action != "revoke" && v.KeyID != key {
		return out, ErrNotFound
	}
	if action == "review" && (in.Review == nil || in.Review.RequestSHA256 != v.RequestSHA256) {
		return out, ErrConflict
	}
	if action != "review" && action != "finish" && (p.Status != "running" || !time.Now().Before(p.Deadline)) {
		return out, ErrConflict
	}
	switch action {
	case "review":
	case "approve", "revoke":
		if in.Decision == nil || in.Decision.RequestSHA256 != v.RequestSHA256 {
			return out, ErrConflict
		}
		if action == "revoke" {
			if v.Status != "pending" && v.Status != "approved" && v.Status != "running" && v.Status != "revoked" {
				return out, ErrConflict
			}
			v.Status, v.Outcome, p.TestCredential = "revoked", nil, nil
		} else {
			if secret == nil || !toolgrant.ValidCredentialRef(in.Decision.CredentialSecretRef) || v.Status != "pending" && v.Status != "approved" {
				return out, ErrConflict
			}
			binding := &toolPublicationTestCredential{Ref: in.Decision.CredentialSecretRef, SHA256: toolpublication.Digest(secret.Ciphertext)}
			if p.TestCredential != nil && !reflect.DeepEqual(p.TestCredential, binding) {
				return out, ErrConflict
			}
			v.Status, p.TestCredential = "approved", binding
		}
	case "begin":
		if v.Status == "approved" {
			v.Status, out.Created = "running", true
			v.Deadline = minTime(v.Deadline, time.Now().UTC().Add(api.ToolPublicationVerificationTimeout))
		}
	case "dispatch":
		if v.Status != "running" || p.TestCredential == nil || secret == nil || toolpublication.Digest(secret.Ciphertext) != p.TestCredential.SHA256 {
			return out, ErrNotFound
		}
		out.Secret = secret
	case "finish":
		if v.Status != "running" || in.Outcome == nil {
			return out, ErrConflict
		}
		v.Outcome, v.Status = in.Outcome, "failed"
		if in.Outcome.OK {
			candidate := api.ToolPublicationOutcome{Status: "completed", DeploymentID: v.DeploymentID, Catalog: in.Outcome.Catalog, CatalogSHA256: v.CatalogSHA256, ProtocolVerified: in.Outcome.ProtocolVerified, Checks: in.Outcome.Checks}
			if toolpublication.ValidateOutcome(p.Intent, candidate) != nil {
				return out, ErrInvalidArgument
			}
			v.Status = "completed"
		}
		p.TestCredential = nil
	default:
		return out, ErrInvalidArgument
	}
	p.Verification = &v
	out.Verification = v
	return out, nil
}

func (m *MemStore) ChangeToolPublicationVerification(_ context.Context, account, id, key, action string, in ToolPublicationVerificationChange) (ToolPublicationVerificationResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mapKey := account + ":" + id
	record, ok := m.toolPublications[mapKey]
	if !ok {
		return ToolPublicationVerificationResult{}, ErrNotFound
	}
	p := storedToolPublication{ToolPublicationRecord: cloneToolPublication(record), TestCredential: m.toolPublicationTestCredentials[mapKey]}
	v := p.Verification
	if v == nil {
		v = in.Review
	}
	var secret *AppSecret
	if action == "approve" || action == "begin" || action == "dispatch" || action == "review" && p.Verification == nil {
		if err := m.toolPublicationCandidateLocked(account, p.Intent, v); err != nil {
			return ToolPublicationVerificationResult{}, err
		}
	}
	ref := ""
	if p.TestCredential != nil {
		ref = p.TestCredential.Ref
	}
	if action == "approve" && in.Decision != nil {
		ref = in.Decision.CredentialSecretRef
	}
	if action == "approve" || action == "dispatch" {
		if v == nil || !toolgrant.ValidCredentialRef(ref) {
			return ToolPublicationVerificationResult{}, ErrInvalidArgument
		}
		s, found := m.secrets[secretKey{AppID: v.AppID, Scope: api.DefaultEnvScope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix)}]
		if !found || s.AccountID != account || s.ManagedPostgresBindingID != "" || s.ManagedObjectStorageCredentialID != "" || s.ManagedCredentialRef != "" {
			return ToolPublicationVerificationResult{}, ErrNotFound
		}
		secret = &s
	}
	out, err := changeToolVerification(&p, key, action, in, secret)
	if err != nil {
		return out, err
	}
	m.toolPublications[mapKey] = cloneToolPublication(p.ToolPublicationRecord)
	if m.toolPublicationTestCredentials == nil {
		m.toolPublicationTestCredentials = map[string]*toolPublicationTestCredential{}
	}
	m.toolPublicationTestCredentials[mapKey] = p.TestCredential
	return out, nil
}

func (m *MemStore) toolPublicationCandidateLocked(account string, intent api.ToolPublicationIntent, v *api.ToolPublicationVerification) error {
	if !validVerificationTarget(v) {
		return ErrInvalidArgument
	}
	if !m.accounts[account].MayDeploy() {
		return ErrNotFound
	}
	k, ok := m.keys[v.KeyID]
	_, limited := m.keyProjectLimits[v.KeyID]
	a, appOK := m.apps[v.AppID]
	d, depOK := m.deployments[v.DeploymentID]
	if !ok || limited || k.AccountID != account || k.Status != "active" && k.Status != "grace" || k.RevokedAt != nil || k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt) || !slices.Contains(k.Scopes, api.ScopeDeployWrite) && !slices.Contains(k.Scopes, api.ScopeAdmin) || !m.delegatedPersonalOrgLocked(account, k.OrgID) || !appOK || a.AccountID != account || a.Slug != intent.App || !m.delegatedPersonalOrgLocked(account, a.OrgID) || !depOK || d.AppID != v.AppID || d.Status != DeployLive || d.TrafficPercent != 0 || d.SourceSHA256 != v.DeploymentSourceSHA256 || d.Scope != "" && d.Scope != api.DefaultEnvScope {
		return ErrNotFound
	}
	serving := ""
	for _, candidate := range m.deployments {
		if candidate.AppID == v.AppID && candidate.Status == DeployLive && candidate.TrafficPercent > 0 && (candidate.Scope == "" || candidate.Scope == api.DefaultEnvScope) {
			if serving != "" || candidate.TrafficPercent != 100 {
				return ErrConflict
			}
			serving = candidate.ID
		}
	}
	if serving != intent.ExpectedServingDeploymentID {
		return ErrConflict
	}
	return nil
}

func validVerificationTarget(v *api.ToolPublicationVerification) bool {
	return v != nil && toolgrant.ValidResourceID(v.KeyID) && toolgrant.ValidResourceID(v.AppID) && toolgrant.ValidResourceID(v.DeploymentID) && toolpublication.ValidID(v.PublicationID) && toolpublication.ValidID(v.DeploymentSourceSHA256) && toolpublication.ValidID(v.CatalogSHA256)
}

// Keep JSON decoding identical to public journal reads while retaining only
// state-owned approval metadata for the pending bounded verification attempt.
func decodeStoredToolPublication(data []byte) (storedToolPublication, error) {
	var out storedToolPublication
	err := json.Unmarshal(data, &out)
	return out, err
}
