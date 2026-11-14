package state

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// This optional store serializes owner decisions and each credential dispatch
// with revocation/rotation. A dispatched HTTP request may finish after revoke.
type ToolPublicationVerificationStore interface {
	ChangeToolPublicationVerification(context.Context, string, string, string, string, ToolPublicationVerificationChange) (ToolPublicationVerificationResult, error)
}

type ToolPublicationVerificationChange struct {
	Role              string
	ReadinessSubject  *MCPReadinessSubject
	ReadinessEvidence *api.MCPReadinessEvidence
	Review            *api.ToolPublicationVerification
	Decision          *api.ToolPublicationVerificationDecision
	Outcome           *api.ToolPublicationVerificationOutcome
	LeaseID           string
}

type ToolPublicationVerificationResult struct {
	ReadinessSubject  *MCPReadinessSubject
	ReadinessEvidence *api.MCPReadinessEvidence
	Verification      api.ToolPublicationVerification
	Created           bool
	Secret            *AppSecret           // internal dispatch only; never serialize this result
	Connection        *ToolOAuthConnection // internal credential state, never serialize
	RenewalFresh      bool
}

type toolPublicationTestCredential struct {
	ReadinessApproved bool   `json:"readiness_approved,omitempty"`
	Ref               string `json:"ref"`
	SHA256            string `json:"sha256"`
	ConnectionID      string `json:"connection_id,omitempty"`
}

// Private approval metadata shares the existing bounded publication payload.
// Public reads decode only ToolPublicationRecord and cannot expose this field.
type storedToolPublication struct {
	api.ToolPublicationRecord
	TestCredential *toolPublicationTestCredential `json:"verification_credential,omitempty"`
}

func changeToolVerification(p *storedToolPublication, key, action string, in ToolPublicationVerificationChange, secret *AppSecret, connection *ToolOAuthConnection) (ToolPublicationVerificationResult, error) {
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
			if v.Status != "pending" && v.Status != "approved" {
				return out, ErrConflict
			}
			binding, err := publicationCredentialBinding(*p, *in.Decision, secret, connection)
			if err != nil {
				return out, err
			}
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
	case "dispatch", "renew", "renew_dispatch":
		if v.Status != "running" || p.TestCredential == nil {
			return out, ErrNotFound
		}
		if p.TestCredential.ConnectionID != "" {
			if connection == nil || connection.Record.ID != p.TestCredential.ConnectionID || connection.Record.BindingSHA256 != p.TestCredential.SHA256 {
				return out, ErrNotFound
			}
			if err := publicationConnectionAuthority(*p, *connection); err != nil {
				return out, err
			}
			if action == "dispatch" && !toolOAuthUsable(*connection, time.Now()) {
				return out, ErrToolOAuthReconnect
			}
			if action == "renew_dispatch" && (connection.Record.State != "refreshing" || in.LeaseID == "" || connection.LeaseID != in.LeaseID || connection.LeaseUntil == nil || !time.Now().Before(*connection.LeaseUntil)) {
				return out, ErrNotFound
			}
			out.Connection = connection
		} else {
			if action == "renew_dispatch" || secret == nil || toolpublication.Digest(secret.Ciphertext) != p.TestCredential.SHA256 {
				return out, ErrNotFound
			}
			out.Secret = secret
		}
	case "readiness_dispatch", "readiness_record", "readiness_assert":
		if v.Status != "running" || p.Intent.Readiness == nil || p.TestCredential == nil || !p.TestCredential.ReadinessApproved {
			return out, ErrNotFound
		}
		if action != "readiness_assert" {
			found := false
			for _, role := range p.Intent.Readiness.Roles {
				found = found || role.Name == in.Role
			}
			if !found {
				return out, ErrInvalidArgument
			}
		}
	case "finish":
		if v.Status != "running" || in.Outcome == nil {
			return out, ErrConflict
		}
		v.Outcome, v.Status = in.Outcome, "failed"
		if in.Outcome.OK {
			if !toolpublication.ReadinessResultsMatch(p.Intent.Readiness, in.Outcome.Readiness) {
				return out, ErrInvalidArgument
			}
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
	if publicationAuthorityAction(action, in) {
		if err := m.toolPublicationCandidateLocked(account, p.Intent, v); err != nil {
			return ToolPublicationVerificationResult{}, err
		}
	}
	var readiness *MCPReadinessSubject
	if publicationAuthorityAction(action, in) {
		var err error
		readiness, err = m.publicationReadinessLocked(p, v)
		if err != nil {
			return ToolPublicationVerificationResult{}, err
		}
	}
	ref := ""
	connectionID := ""
	if p.TestCredential != nil {
		ref = p.TestCredential.Ref
		connectionID = p.TestCredential.ConnectionID
	}
	if action == "approve" && in.Decision != nil {
		ref = in.Decision.CredentialSecretRef
		connectionID = in.Decision.CredentialConnectionID
	}
	var connection *ToolOAuthConnection
	if action == "approve" || action == "dispatch" || action == "renew" || action == "renew_dispatch" {
		if connectionID != "" {
			c, found := m.toolOAuthConnections[account+":"+connectionID]
			if !found {
				return ToolPublicationVerificationResult{}, ErrNotFound
			}
			if err := m.toolOAuthAuthorityLocked(c.Record); err != nil {
				return ToolPublicationVerificationResult{}, err
			}
			copy := cloneToolOAuth(c)
			connection = &copy
		} else {
			if v == nil || !toolgrant.ValidCredentialRef(ref) {
				return ToolPublicationVerificationResult{}, ErrInvalidArgument
			}
			s, found := m.secrets[secretKey{AppID: v.AppID, Scope: api.DefaultEnvScope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix)}]
			if !found || s.AccountID != account || s.ManagedPostgresBindingID != "" || s.ManagedObjectStorageCredentialID != "" || s.ManagedCredentialRef != "" {
				return ToolPublicationVerificationResult{}, ErrNotFound
			}
			secret = &s
		}
	}
	out, err := changeToolVerification(&p, key, action, in, secret, connection)
	if err != nil {
		return out, err
	}
	out.ReadinessSubject = readiness
	if action == "readiness_record" {
		if readiness == nil || in.ReadinessSubject == nil || in.ReadinessEvidence == nil || in.ReadinessSubject.AccountID != account || in.ReadinessSubject.DeploymentID != v.DeploymentID || in.ReadinessEvidence.Role != in.Role || !validateReadinessEvidence(*in.ReadinessSubject, *in.ReadinessEvidence) {
			return out, ErrInvalidArgument
		}
		saved, err := m.recordMCPReadinessLocked(*in.ReadinessSubject, *in.ReadinessEvidence)
		out.ReadinessEvidence = &saved
		return out, err
	}
	if (action == "readiness_assert" || action == "finish" && in.Outcome != nil && in.Outcome.OK) && !publicationReadinessPassed(p, readiness, m.publicationReadinessEvidenceLocked(v.DeploymentID), in.Outcome) {
		return out, ErrConflict
	}
	if action == "renew" && connection != nil {
		claim, claimErr := claimToolOAuth(*connection, time.Now().UTC())
		if claim.Connection.Record.ID != "" {
			m.toolOAuthConnections[account+":"+connectionID] = cloneToolOAuth(claim.Connection)
		}
		out.Connection, out.RenewalFresh = &claim.Connection, claim.Fresh
		return out, claimErr
	}
	m.toolPublications[mapKey] = cloneToolPublication(p.ToolPublicationRecord)
	if m.toolPublicationTestCredentials == nil {
		m.toolPublicationTestCredentials = map[string]*toolPublicationTestCredential{}
	}
	m.toolPublicationTestCredentials[mapKey] = p.TestCredential
	return out, nil
}

func publicationCredentialBinding(p storedToolPublication, in api.ToolPublicationVerificationDecision, secret *AppSecret, connection *ToolOAuthConnection) (*toolPublicationTestCredential, error) {
	if (p.Intent.Readiness != nil) != in.ApproveReadiness {
		return nil, ErrInvalidArgument
	}
	if in.CredentialConnectionID != "" {
		if in.CredentialSecretRef != "" || !toolgrant.ValidID(in.CredentialConnectionID) || connection == nil || connection.Record.ID != in.CredentialConnectionID {
			return nil, ErrInvalidArgument
		}
		if err := publicationConnectionAuthority(p, *connection); err != nil {
			return nil, err
		}
		return &toolPublicationTestCredential{ConnectionID: connection.Record.ID, SHA256: connection.Record.BindingSHA256, ReadinessApproved: in.ApproveReadiness}, nil
	}
	if secret == nil || !toolgrant.ValidCredentialRef(in.CredentialSecretRef) {
		return nil, ErrInvalidArgument
	}
	return &toolPublicationTestCredential{Ref: in.CredentialSecretRef, SHA256: toolpublication.Digest(secret.Ciphertext), ReadinessApproved: in.ApproveReadiness}, nil
}

func publicationConnectionAuthority(p storedToolPublication, c ToolOAuthConnection) error {
	r, auth := c.Record, p.Intent.Auth
	if p.Verification == nil || auth == nil || toolconnection.ValidateRecord(r) != nil || r.RevokedAt != nil || r.AccountID != p.AccountID || r.AppID != p.Verification.AppID || r.Intent.App != p.Intent.App || r.Intent.Scope != api.DefaultEnvScope || r.Intent.Issuer != auth.Issuer || r.Intent.Resource != auth.Resource || !toolconnection.Covers(r.Intent.Scopes, auth.Scopes) {
		return ErrNotFound
	}
	for _, scopes := range p.Intent.ToolScopes {
		if !toolconnection.Covers(r.Intent.Scopes, scopes) {
			return ErrNotFound
		}
	}
	if r.State == "reconnect_required" || r.Intent.Guided && r.Generation == 0 {
		return ErrToolOAuthReconnect
	}
	return nil
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
