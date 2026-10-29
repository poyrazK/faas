package outbound

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const (
	// WorkloadIdentityHeader carries a vmmd-signed assertion to outboundd.
	// It is never forwarded to a provider.
	WorkloadIdentityHeader = workloadidentity.OutboundHeader
	// ExecutionIdentityHeader carries a host-broker-signed assertion for one
	// active Runs lease. It is never forwarded to the guest or provider.
	ExecutionIdentityHeader = "X-Gregale-Execution-Identity"
	identityAudiencePrefix  = "gregale:outbound:"
	maxIdentityTokenBytes   = 8192
	maxIdentityTTL          = 5 * time.Minute
	identityClockSkew       = 5 * time.Second
)

var ErrInvalidWorkloadIdentity = errors.New("invalid outbound workload identity")

// WorkloadIdentity is the authenticated caller, not a guest-supplied app ID.
type WorkloadIdentity struct {
	AccountID  string
	AppID      string
	InstanceID string
}

type IdentityVerifier interface {
	Verify(rawToken, integrationID string) (WorkloadIdentity, error)
}

// ExecutionIdentity identifies one disposable Run and the scheduler lease
// that currently owns it. LeaseToken is internal capability material.
type ExecutionIdentity struct {
	AccountID   string
	ExecutionID string
	LeaseToken  string
}

// AgentRunIdentity identifies one agent run (ADR-20261027090010 §7) and agentd's
// lease on it. LeaseToken is internal capability material.
type AgentRunIdentity struct {
	AccountID    string
	RunID        string
	LeaseToken   string
	TenantID     string
	AgentID      string
	AgentVersion int
}

// AgentRunGrant is what an agent run may do with one integration: the
// tenant its calls are attributed to and its model spend cap.
type AgentRunGrant struct {
	TenantID           string
	AIBudgetMillicents int64
}

// AgentRunAuthorizer checks, for every request, that the run is running
// under the identity's lease and that its agent version names the
// integration. It must fail closed on store errors.
type AgentRunAuthorizer interface {
	AuthorizeAgentRun(ctx context.Context, identity AgentRunIdentity, integrationID string) (AgentRunGrant, bool, error)
}

type ExecutionIdentityVerifier interface {
	VerifyExecution(rawToken, integrationID string) (ExecutionIdentity, error)
}

// ExecutionAuthorizer must check the current execution lease and current
// integration grant for every outbound request. It must fail closed on store
// errors; a signed assertion alone is not a durable authorization.
type ExecutionAuthorizer interface {
	AuthorizeExecution(context.Context, ExecutionIdentity, string) (bool, error)
}

// WorkloadIdentityVerifier trusts a locally provisioned public JWKS. No
// caller-controlled URL or network fetch participates in authentication.
type WorkloadIdentityVerifier struct {
	keys   map[string]*rsa.PublicKey
	issuer string
	now    func() time.Time
}

func NewWorkloadIdentityVerifier(jwksJSON []byte, issuer string) (*WorkloadIdentityVerifier, error) {
	parsedIssuer, err := url.Parse(issuer)
	if err != nil || parsedIssuer.Scheme != "https" || parsedIssuer.Host == "" || parsedIssuer.User != nil || parsedIssuer.RawQuery != "" || parsedIssuer.Fragment != "" || strings.TrimSpace(issuer) != issuer {
		return nil, errors.New("outbound workload identity issuer must be an HTTPS URL")
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(jwksJSON, &set); err != nil {
		return nil, fmt.Errorf("outbound workload identity JWKS: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("outbound workload identity JWKS has no keys")
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, key := range set.Keys {
		public, ok := key.Key.(*rsa.PublicKey)
		if !ok || !key.IsPublic() || !key.Valid() || key.KeyID == "" || key.Algorithm != string(jose.RS256) || key.Use != "sig" || public.N.BitLen() < 2048 {
			return nil, errors.New("outbound workload identity JWKS contains an invalid signing key")
		}
		if _, exists := keys[key.KeyID]; exists {
			return nil, errors.New("outbound workload identity JWKS contains duplicate key IDs")
		}
		keys[key.KeyID] = public
	}
	return &WorkloadIdentityVerifier{keys: keys, issuer: issuer, now: time.Now}, nil
}

func (v *WorkloadIdentityVerifier) Verify(rawToken, integrationID string) (WorkloadIdentity, error) {
	if integrationID == "" || identityAudiencePrefix+integrationID == workloadidentity.OutboundRelayAudience {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	return v.verifyAppAudience(rawToken, identityAudiencePrefix+integrationID)
}

// VerifyRelay accepts only the connection-scoped assertion vmmd attaches to
// an app relay connection (ADR-989). It is never valid for one integration.
func (v *WorkloadIdentityVerifier) VerifyRelay(rawToken string) (WorkloadIdentity, error) {
	return v.verifyAppAudience(rawToken, workloadidentity.OutboundRelayAudience)
}

func (v *WorkloadIdentityVerifier) verifyAppAudience(rawToken, audience string) (WorkloadIdentity, error) {
	if v == nil || len(rawToken) == 0 || len(rawToken) > maxIdentityTokenBytes || audience == "" {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	token, err := jwt.ParseSigned(rawToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(token.Headers) != 1 {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	header := token.Headers[0]
	if header.Algorithm != string(jose.RS256) || header.KeyID == "" {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	key, ok := v.keys[header.KeyID]
	if !ok {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	var claims workloadidentity.Claims
	if err := token.Claims(key, &claims); err != nil {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	now := v.now()
	if len(claims.Audience) != 1 || claims.Audience[0] != audience || claims.AccountID == "" || claims.AppID == "" || claims.InstanceID == "" || !claims.SubjectMatches() || claims.ID == "" || claims.IssuedAt == nil || claims.NotBefore == nil || claims.Expiry == nil {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	if claims.Expiry.Time().Before(claims.IssuedAt.Time()) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > maxIdentityTTL || claims.NotBefore.Time().After(claims.IssuedAt.Time()) {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{
		Issuer: v.issuer, Subject: workloadidentity.Subject(claims.AppID, claims.Environment),
		AnyAudience: jwt.Audience{audience}, Time: now,
	}, identityClockSkew); err != nil {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	return WorkloadIdentity{AccountID: claims.AccountID, AppID: claims.AppID, InstanceID: claims.InstanceID}, nil
}

var _ IdentityVerifier = (*WorkloadIdentityVerifier)(nil)

// VerifyExecution accepts only execution-subject assertions. App workload
// tokens cannot be used as Run identities, even when signed by the same key.
func (v *WorkloadIdentityVerifier) VerifyExecution(rawToken, integrationID string) (ExecutionIdentity, error) {
	if integrationID == "" || identityAudiencePrefix+integrationID == workloadidentity.OutboundRelayAudience {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	return v.verifyExecutionAudience(rawToken, identityAudiencePrefix+integrationID)
}

// VerifyExecutionRelay accepts only the connection-scoped assertion vmmd
// attaches to a Run's relay connection (ADR-990).
func (v *WorkloadIdentityVerifier) VerifyExecutionRelay(rawToken string) (ExecutionIdentity, error) {
	return v.verifyExecutionAudience(rawToken, workloadidentity.OutboundRelayAudience)
}

func (v *WorkloadIdentityVerifier) verifyExecutionAudience(rawToken, audience string) (ExecutionIdentity, error) {
	if v == nil || len(rawToken) == 0 || len(rawToken) > maxIdentityTokenBytes || audience == "" {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	var claims workloadidentity.ExecutionClaims
	if err := v.signedClaims(rawToken, &claims); err != nil {
		return ExecutionIdentity{}, err
	}
	now := v.now()
	if len(claims.Audience) != 1 || claims.Audience[0] != audience || claims.AccountID == "" || claims.ExecutionID == "" || claims.LeaseToken == "" ||
		claims.Subject != "execution:"+claims.ExecutionID || claims.ID == "" || claims.IssuedAt == nil || claims.NotBefore == nil || claims.Expiry == nil {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	if _, err := uuid.Parse(claims.AccountID); err != nil {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	if _, err := uuid.Parse(claims.ExecutionID); err != nil {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	if _, err := uuid.Parse(claims.LeaseToken); err != nil {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	if claims.Expiry.Time().Before(claims.IssuedAt.Time()) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > maxIdentityTTL || claims.NotBefore.Time().After(claims.IssuedAt.Time()) {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{
		Issuer: v.issuer, Subject: "execution:" + claims.ExecutionID,
		AnyAudience: jwt.Audience{audience}, Time: now,
	}, identityClockSkew); err != nil {
		return ExecutionIdentity{}, ErrInvalidWorkloadIdentity
	}
	return ExecutionIdentity{AccountID: claims.AccountID, ExecutionID: claims.ExecutionID, LeaseToken: claims.LeaseToken}, nil
}

var _ ExecutionIdentityVerifier = (*WorkloadIdentityVerifier)(nil)

// signedClaims checks an RS256 signature by a provisioned key and decodes
// the claims; callers check the claims themselves.
func (v *WorkloadIdentityVerifier) signedClaims(rawToken string, claims any) error {
	token, err := jwt.ParseSigned(rawToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(token.Headers) != 1 {
		return ErrInvalidWorkloadIdentity
	}
	header := token.Headers[0]
	if header.Algorithm != string(jose.RS256) || header.KeyID == "" {
		return ErrInvalidWorkloadIdentity
	}
	key, ok := v.keys[header.KeyID]
	if !ok {
		return ErrInvalidWorkloadIdentity
	}
	if err := token.Claims(key, claims); err != nil {
		return ErrInvalidWorkloadIdentity
	}
	return nil
}

// VerifyAgentRunRelay accepts only the connection-scoped assertion vmmd
// attaches to an agent run's relay connection (ADR-20261027090010 §7). App and Run
// assertions never pass: the subject, the run ID and the lease must match.
func (v *WorkloadIdentityVerifier) VerifyAgentRunRelay(rawToken string) (AgentRunIdentity, error) {
	if v == nil || len(rawToken) == 0 || len(rawToken) > maxIdentityTokenBytes {
		return AgentRunIdentity{}, ErrInvalidWorkloadIdentity
	}
	var claims workloadidentity.AgentRunClaims
	if err := v.signedClaims(rawToken, &claims); err != nil {
		return AgentRunIdentity{}, err
	}
	audience := workloadidentity.OutboundRelayAudience
	if len(claims.Audience) != 1 || claims.Audience[0] != audience || claims.RunID == "" ||
		claims.Subject != workloadidentity.AgentRunSubjectPrefix+claims.RunID || claims.ID == "" ||
		claims.IssuedAt == nil || claims.NotBefore == nil || claims.Expiry == nil || claims.AgentVersion < 1 {
		return AgentRunIdentity{}, ErrInvalidWorkloadIdentity
	}
	for _, id := range []string{claims.AccountID, claims.RunID, claims.LeaseToken, claims.AgentID} {
		if _, err := uuid.Parse(id); err != nil {
			return AgentRunIdentity{}, ErrInvalidWorkloadIdentity
		}
	}
	if claims.TenantID != "" {
		if _, err := uuid.Parse(claims.TenantID); err != nil {
			return AgentRunIdentity{}, ErrInvalidWorkloadIdentity
		}
	}
	if claims.Expiry.Time().Before(claims.IssuedAt.Time()) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > maxIdentityTTL ||
		claims.NotBefore.Time().After(claims.IssuedAt.Time()) {
		return AgentRunIdentity{}, ErrInvalidWorkloadIdentity
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{Issuer: v.issuer, Subject: claims.Subject,
		AnyAudience: jwt.Audience{audience}, Time: v.now()}, identityClockSkew); err != nil {
		return AgentRunIdentity{}, ErrInvalidWorkloadIdentity
	}
	return AgentRunIdentity{AccountID: claims.AccountID, RunID: claims.RunID, LeaseToken: claims.LeaseToken,
		TenantID: claims.TenantID, AgentID: claims.AgentID, AgentVersion: claims.AgentVersion}, nil
}
