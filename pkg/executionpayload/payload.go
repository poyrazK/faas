// Package executionpayload owns the authenticated source/input envelope used
// by disposable one-shot executions. The scheduler is the only production
// caller that opens the envelope; apid can use Seal to persist one without
// importing scheduler or vmmd packages.
package executionpayload

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const (
	// CurrentVersion is the authenticated payload wire version. A future
	// incompatible envelope must use a new version rather than changing the
	// meaning of an existing ciphertext.
	CurrentVersion uint16 = 1
	// Namespace prevents an execution ciphertext from being replayed as a
	// different secretbox byte-envelope (for example a webhook secret).
	Namespace = "execution_payload"
)

var (
	ErrInvalid        = errors.New("executionpayload: invalid payload")
	ErrKIDMismatch    = errors.New("executionpayload: payload key id does not match host identity")
	ErrWrongNamespace = errors.New("executionpayload: unexpected payload namespace")
)

// Envelope is the plaintext shape sealed at rest. Source and input are
// deliberately together so the scheduler opens one authenticated object and
// never has to reconcile independently sealed fields.
type Envelope struct {
	Version    uint16              `json:"version"`
	Source     string              `json:"source,omitempty"`
	Entrypoint string              `json:"entrypoint,omitempty"`
	Files      []api.ExecutionFile `json:"files,omitempty"`
	Input      json.RawMessage     `json:"input"`
}

// DecodedPayload is the authenticated plaintext handed to the scheduler
// immediately before guest dispatch. Callers must discard it after use.
type DecodedPayload struct {
	Source     string
	Entrypoint string
	Files      []api.ExecutionFile
	Input      json.RawMessage
}

// Validate enforces the same hard limits as the guest protocol before any
// plaintext is handed to the scheduler. Plan-specific limits were already
// checked by apid; these bounds prevent a malformed ciphertext from bypassing
// the host/guest protocol's allocation guard.
func (e Envelope) Validate() error {
	if e.Version != CurrentVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalid, e.Version)
	}
	if strings.TrimSpace(e.Source) != "" {
		if strings.ContainsRune(e.Source, '\x00') || len(e.Source) > api.ExecutionPlaintextFieldMaxBytes || len(e.Files) != 0 || e.Entrypoint != "" {
			return fmt.Errorf("%w: source is outside hard bounds or mixed with a bundle", ErrInvalid)
		}
	} else if err := api.ValidateExecutionBundle(e.Entrypoint, e.Files, api.ExecutionPlaintextFieldMaxBytes); err != nil {
		return fmt.Errorf("%w: bundle is invalid: %v", ErrInvalid, err)
	}
	if len(e.Input) == 0 || len(e.Input) > api.ExecutionPlaintextFieldMaxBytes || !json.Valid(e.Input) {
		return fmt.Errorf("%w: input is missing, too large, or invalid JSON", ErrInvalid)
	}
	return nil
}

// Seal validates and age-encrypts one source/input envelope. The returned
// ciphertext is suitable for execution_payloads.sealed_payload.
func Seal(recipient *age.X25519Recipient, source string, input json.RawMessage) ([]byte, error) {
	return SealRequest(recipient, api.ResolvedExecutionRequest{Source: source, Input: input})
}

// SealRequest validates and age-encrypts either the legacy source string or a
// multi-file ephemeral bundle together with its JSON input.
func SealRequest(recipient *age.X25519Recipient, request api.ResolvedExecutionRequest) ([]byte, error) {
	if recipient == nil {
		return nil, fmt.Errorf("%w: recipient is nil", ErrInvalid)
	}
	input := request.Input
	if input == nil {
		input = json.RawMessage("null")
	}
	envelope := Envelope{
		Version:    CurrentVersion,
		Source:     request.Source,
		Entrypoint: request.Entrypoint,
		Files:      cloneFiles(request.Files),
		Input:      append(json.RawMessage(nil), input...),
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal envelope: %w", ErrInvalid, err)
	}
	sealed, err := secretbox.SealBytes(recipient, Namespace, plaintext, api.ExecutionSealedPayloadMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("executionpayload: seal envelope: %w", err)
	}
	return sealed, nil
}

// Decode opens one ciphertext under the current/previous host identities and
// returns the source/input only after authenticating both the age envelope and
// the stamped key id. The returned error intentionally carries no ciphertext,
// source, input, storage path, or key material.
func Decode(ctx context.Context, identities []*age.X25519Identity, sealed []byte, kid string) (string, json.RawMessage, error) {
	decoded, err := DecodeRequest(ctx, identities, sealed, kid)
	if err != nil {
		return "", nil, err
	}
	return decoded.Source, decoded.Input, nil
}

// DecodeRequest opens one ciphertext and returns the authenticated source
// contract, including an optional file bundle.
func DecodeRequest(ctx context.Context, identities []*age.X25519Identity, sealed []byte, kid string) (DecodedPayload, error) {
	if err := ctx.Err(); err != nil {
		return DecodedPayload{}, err
	}
	if len(sealed) == 0 || len(sealed) > api.ExecutionSealedPayloadMaxBytes {
		return DecodedPayload{}, ErrInvalid
	}
	identity := identityForKID(identities, kid)
	if identity == nil {
		return DecodedPayload{}, ErrKIDMismatch
	}
	namespace, plaintext, err := secretbox.OpenBytes(identity, sealed)
	if err != nil {
		if opensWithAnotherIdentity(identities, identity, sealed) {
			return DecodedPayload{}, ErrKIDMismatch
		}
		return DecodedPayload{}, ErrInvalid
	}
	if subtle.ConstantTimeCompare([]byte(namespace), []byte(Namespace)) != 1 {
		return DecodedPayload{}, ErrWrongNamespace
	}
	if err := ctx.Err(); err != nil {
		clear(plaintext)
		return DecodedPayload{}, err
	}
	envelope, err := decodeEnvelope(plaintext)
	clear(plaintext)
	if err != nil {
		return DecodedPayload{}, err
	}
	return DecodedPayload{
		Source: envelope.Source, Entrypoint: envelope.Entrypoint,
		Files: cloneFiles(envelope.Files), Input: append(json.RawMessage(nil), envelope.Input...),
	}, nil
}

// NewDecoder adapts Decode to sched.ExecutionPayloadDecoder without making
// this package depend on scheduler internals. It snapshots the identity slice
// so callers cannot mutate the key set while a claim is being processed.
func NewDecoder(identities []*age.X25519Identity) func(context.Context, []byte, string) (DecodedPayload, error) {
	keys := append([]*age.X25519Identity(nil), identities...)
	return func(ctx context.Context, sealed []byte, kid string) (DecodedPayload, error) {
		return DecodeRequest(ctx, keys, sealed, kid)
	}
}

func cloneFiles(files []api.ExecutionFile) []api.ExecutionFile {
	cloned := make([]api.ExecutionFile, len(files))
	for i, file := range files {
		cloned[i] = api.ExecutionFile{Path: file.Path, Content: append([]byte(nil), file.Content...)}
	}
	return cloned
}

func identityForKID(identities []*age.X25519Identity, kid string) *age.X25519Identity {
	if strings.TrimSpace(kid) != kid || kid == "" || len(kid) > 255 {
		return nil
	}
	for _, identity := range identities {
		if identity == nil {
			continue
		}
		candidate := identity.Recipient().String()
		if len(candidate) == len(kid) && subtle.ConstantTimeCompare([]byte(candidate), []byte(kid)) == 1 {
			return identity
		}
	}
	return nil
}

func opensWithAnotherIdentity(identities []*age.X25519Identity, selected *age.X25519Identity, sealed []byte) bool {
	for _, identity := range identities {
		if identity == nil || identity == selected {
			continue
		}
		if _, _, err := secretbox.OpenBytes(identity, sealed); err == nil {
			return true
		}
	}
	return false
}

func decodeEnvelope(plaintext []byte) (Envelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Envelope{}, ErrInvalid
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, ErrInvalid
	}
	return envelope, nil
}
