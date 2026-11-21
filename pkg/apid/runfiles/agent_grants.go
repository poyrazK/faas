package runfiles

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/state"
)

const agentInputPrefix = "agf1."

// AgentFileScope binds a grant to one delegated task invocation. The scope
// authenticates the ciphertext without exposing these identifiers in the token.
type AgentFileScope struct {
	AccountID  string
	EndpointID string
	ThreadID   string
	ToolCallID string
	TaskName   string
	Version    int
	// Omission preserves the associated data of already issued task grants.
	StepLabel string `json:",omitempty"`
}

type agentFileClaim struct {
	FileID     string   `json:"f"`
	Path       []byte   `json:"p"`
	Generation [32]byte `json:"g"`
	Expires    int64    `json:"x"`
}

type AgentFileSigner struct{ seal cipher.AEAD }

func NewAgentFileSigner(bindingKey []byte) (*AgentFileSigner, error) {
	if len(bindingKey) < 32 {
		return nil, errors.New("agent file signing key is unavailable")
	}
	key, err := hkdf.Key(sha256.New, bindingKey, nil, "gregale scoped Agent file input v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	seal, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &AgentFileSigner{seal: seal}, nil
}

func (s *AgentFileSigner) Issue(file state.ExecutionFile, scope AgentFileScope, path string, now time.Time) (api.AgentExecutionFileGrant, error) {
	if s == nil {
		return api.AgentExecutionFileGrant{}, api.ErrCapacity("Agent file grants are unavailable")
	}
	if !runfile.ValidPath(path, api.ExecutionBundleMaxPathSize) {
		return api.AgentExecutionFileGrant{}, api.ErrValidation("path must be a normalized relative Run input path")
	}
	if file.AccountID != scope.AccountID || !availableAgentFile(file, now) {
		return api.AgentExecutionFileGrant{}, agentFileUnavailable()
	}
	expires := now.Add(api.AgentExecutionFileGrantTTL)
	if file.ExpiresAt.Before(expires) {
		expires = file.ExpiresAt
	}
	claim := agentFileClaim{FileID: file.ID, Path: []byte(path), Generation: sha256.Sum256([]byte(file.StorageKey)), Expires: expires.UnixNano()}
	raw, err := json.Marshal(claim)
	if err != nil {
		return api.AgentExecutionFileGrant{}, err
	}
	binding, err := json.Marshal(scope)
	if err != nil {
		return api.AgentExecutionFileGrant{}, err
	}
	sealed := s.seal.Seal(nil, nil, raw, binding) //nolint:gosec // G407: NewGCMWithRandomNonce generates and prepends a random nonce.
	token := agentInputPrefix + base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > api.AgentExecutionFileGrantMaxBytes {
		return api.AgentExecutionFileGrant{}, api.ErrValidation("the encoded Run input path is too large")
	}
	return api.AgentExecutionFileGrant{GrantToken: token, ExpiresAt: expires.UTC()}, nil
}

// AgentFilePin identifies exactly the upload generation delegated to a step.
// It belongs in the sealed pipeline plan and never in a public receipt.
type AgentFilePin struct {
	Input      api.ExecutionFileInput `json:"input"`
	Generation [32]byte               `json:"generation"`
}

func (p AgentFilePin) Matches(ref runfile.Ref) bool {
	return ref.ID == p.Input.FileID && ref.Path == p.Input.Path && sha256.Sum256([]byte(ref.StorageKey)) == p.Generation
}

// Resolve runs inside admission, after checking for an already admitted call.
// The ready file must still be the same upload generation the owner delegated.
func (s *AgentFileSigner) Resolve(ctx context.Context, reader state.ExecutionFileAdmissionReader, scope AgentFileScope, token string, now time.Time) (api.ExecutionFileInput, error) {
	pin, err := s.Pin(ctx, reader, scope, token, now)
	return pin.Input, err
}

// Pin verifies the grant before a new task or pipeline is admitted.
func (s *AgentFileSigner) Pin(ctx context.Context, reader state.ExecutionFileAdmissionReader, scope AgentFileScope, token string, now time.Time) (AgentFilePin, error) {
	if s == nil || reader == nil {
		return AgentFilePin{}, api.ErrCapacity("Agent file admission is unavailable")
	}
	encoded, ok := strings.CutPrefix(token, agentInputPrefix)
	if !ok || len(token) > api.AgentExecutionFileGrantMaxBytes {
		return AgentFilePin{}, agentFileUnavailable()
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return AgentFilePin{}, agentFileUnavailable()
	}
	binding, err := json.Marshal(scope)
	if err != nil {
		return AgentFilePin{}, err
	}
	raw, err := s.seal.Open(nil, nil, sealed, binding)
	if err != nil {
		return AgentFilePin{}, agentFileUnavailable()
	}
	var claim agentFileClaim
	if json.Unmarshal(raw, &claim) != nil || now.UnixNano() >= claim.Expires {
		return AgentFilePin{}, agentFileUnavailable()
	}
	file, err := reader.ExecutionFileByID(ctx, scope.AccountID, claim.FileID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return AgentFilePin{}, api.ErrCapacity("Agent file metadata is unavailable")
	}
	if err != nil || file.ID != claim.FileID || file.AccountID != scope.AccountID || !availableAgentFile(file, now) || sha256.Sum256([]byte(file.StorageKey)) != claim.Generation {
		return AgentFilePin{}, agentFileUnavailable()
	}
	return AgentFilePin{Input: api.ExecutionFileInput{FileID: claim.FileID, Path: string(claim.Path)}, Generation: claim.Generation}, nil
}

func availableAgentFile(file state.ExecutionFile, now time.Time) bool {
	return file.Kind == "upload" && file.Status == "ready" && file.ExpiresAt.After(now) && runfile.ValidID(file.ID)
}

func agentFileUnavailable() *api.Problem {
	return api.NewProblem(http.StatusNotFound, "agent_run_file_unavailable", "Agent file grant unavailable", "the grant is invalid, expired or unavailable for this call")
}
