package outbound

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"time"
)

const WorkflowIdentityHeader = "X-Gregale-Workflow-Identity"
const workflowAudiencePrefix = "gregale:workflow:outbound:"
const WorkflowIdentityTTL = 30 * time.Second

var ErrWorkflowNotAuthorized = errors.New("workflow outbound not authorized")

// WorkflowIdentity is private host authorization, never a customer API payload.
type WorkflowIdentity struct {
	AccountID    string `json:"account_id"`
	AppID        string `json:"app_id"`
	RunID        string `json:"run_id"`
	StepName     string `json:"step_name"`
	Attempt      int    `json:"attempt"`
	AttemptToken string `json:"attempt_token"`
}

type workflowClaims struct {
	jwt.Claims
	WorkflowIdentity
	Method     string `json:"method"`
	Path       string `json:"path"`
	BodySHA256 string `json:"body_sha256"`
}

type WorkflowAuthorizer interface {
	AuthorizeWorkflow(context.Context, string, string, string, string, []byte) (WorkflowIdentity, error)
}

func workflowBodyHash(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func validWorkflowIdentity(identity WorkflowIdentity) bool {
	for _, raw := range []string{identity.AccountID, identity.AppID, identity.RunID, identity.AttemptToken} {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || id.String() != raw {
			return false
		}
	}
	return identity.StepName != "" && len(identity.StepName) <= 1024 && identity.Attempt > 0 && identity.Attempt <= 25
}

// MintWorkflowIdentity uses schedd's rotating cluster key with a separate audience
// and subject. Ordinary app, Run, and internal-service assertions cannot authorize
// this request. The body and route are bound to this one active step attempt.
func MintWorkflowIdentity(identity WorkflowIdentity, integrationID, method, path string, body []byte, private ed25519.PrivateKey, keyID string, now time.Time) (string, error) {
	if !validWorkflowIdentity(identity) || len(private) != ed25519.PrivateKeySize || keyID == "" {
		return "", ErrWorkflowNotAuthorized
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: private}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		return "", ErrWorkflowNotAuthorized
	}
	claims := workflowClaims{Claims: jwt.Claims{Issuer: "gregale", Subject: "workflow:" + identity.RunID, Audience: jwt.Audience{workflowAudiencePrefix + integrationID}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(WorkflowIdentityTTL)), ID: uuid.NewString()}, WorkflowIdentity: identity, Method: method, Path: path, BodySHA256: workflowBodyHash(body)}
	return jwt.Signed(signer).Claims(claims).Serialize()
}

func verifyWorkflowIdentity(raw, integrationID, method, path string, body []byte, keyID string, public ed25519.PublicKey, now time.Time) (WorkflowIdentity, error) {
	if raw == "" || len(raw) > 8192 || len(public) != ed25519.PublicKeySize {
		return WorkflowIdentity{}, ErrWorkflowNotAuthorized
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(token.Headers) != 1 || token.Headers[0].KeyID != keyID {
		return WorkflowIdentity{}, ErrWorkflowNotAuthorized
	}
	var claims workflowClaims
	if token.Claims(public, &claims) != nil || !validWorkflowIdentity(claims.WorkflowIdentity) || claims.IssuedAt == nil || claims.NotBefore == nil || claims.Expiry == nil || claims.ID == "" {
		return WorkflowIdentity{}, ErrWorkflowNotAuthorized
	}
	audience := workflowAudiencePrefix + integrationID
	if len(claims.Audience) != 1 || claims.Audience[0] != audience || claims.Subject != "workflow:"+claims.RunID || claims.Method != method || claims.Path != path || claims.BodySHA256 != workflowBodyHash(body) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > WorkflowIdentityTTL || !claims.Expiry.Time().After(claims.IssuedAt.Time()) || claims.NotBefore.Time().After(claims.IssuedAt.Time()) {
		return WorkflowIdentity{}, ErrWorkflowNotAuthorized
	}
	if claims.ValidateWithLeeway(jwt.Expected{Issuer: "gregale", Subject: "workflow:" + claims.RunID, AnyAudience: jwt.Audience{audience}, Time: now}, 5*time.Second) != nil {
		return WorkflowIdentity{}, ErrWorkflowNotAuthorized
	}
	return claims.WorkflowIdentity, nil
}
