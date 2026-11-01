package agents

// Agent callback tokens (ADR-20261030125501 §3). A token names one thread of
// one endpoint's agent inbox, the message kinds it may carry and when it
// expires, and is signed with a key only apid holds. It is the only
// credential a callback needs, so it never names anything a caller could
// widen: a changed field fails the signature.

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// callbackTokenPrefix versions the token format.
const callbackTokenPrefix = "agcb1"

// callbackKeyInfo separates the callback key from every other use of the
// key it is derived from.
const callbackKeyInfo = "gregale agent callback v1"

// ErrCallbackToken is a token that is malformed, forged, for another
// format or expired. Callers answer it like an unknown route.
var ErrCallbackToken = errors.New("agents: invalid callback token")

// CallbackToken is what a callback token grants.
type CallbackToken struct {
	AccountID  string    `json:"a"`
	EndpointID string    `json:"e"`
	ThreadID   string    `json:"t"`
	Kinds      []string  `json:"k"`
	IssuedAt   time.Time `json:"i"`
	ExpiresAt  time.Time `json:"x"`
}

// Allows reports whether the token may carry a message of kind.
func (t CallbackToken) Allows(kind string) bool { return slices.Contains(t.Kinds, kind) }

// CallbackSigner signs and verifies callback tokens.
type CallbackSigner struct {
	key []byte
}

// NewCallbackSigner derives the callback key from secret, apid's session
// binding key, so every apid replica verifies every replica's tokens.
func NewCallbackSigner(secret []byte) (*CallbackSigner, error) {
	if len(secret) < 32 {
		return nil, errors.New("agents: callback signing secret must be at least 32 bytes")
	}
	key, err := hkdf.Key(sha256.New, secret, nil, callbackKeyInfo, 32)
	if err != nil {
		return nil, err
	}
	return &CallbackSigner{key: key}, nil
}

// Sign returns the token for t.
func (s *CallbackSigner) Sign(t CallbackToken) (string, error) {
	body, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	payload := tokenEncoding.EncodeToString(body)
	return callbackTokenPrefix + "." + payload + "." + tokenEncoding.EncodeToString(s.mac(payload)), nil
}

// Verify checks a token's signature and expiry at now and returns what it
// grants.
func (s *CallbackSigner) Verify(token string, now time.Time) (CallbackToken, error) {
	prefix, rest, ok := strings.Cut(token, ".")
	payload, sig, ok2 := strings.Cut(rest, ".")
	if !ok || !ok2 || prefix != callbackTokenPrefix || len(token) > callbackTokenMaxBytes {
		return CallbackToken{}, ErrCallbackToken
	}
	mac, err := tokenEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(mac, s.mac(payload)) {
		return CallbackToken{}, ErrCallbackToken
	}
	body, err := tokenEncoding.DecodeString(payload)
	if err != nil {
		return CallbackToken{}, ErrCallbackToken
	}
	var t CallbackToken
	if json.Unmarshal(body, &t) != nil || t.AccountID == "" || t.EndpointID == "" || !ValidCallbackID(t.ThreadID, api.AgentCallbackThreadIDMaxBytes) ||
		len(t.Kinds) == 0 || !now.Before(t.ExpiresAt) {
		return CallbackToken{}, ErrCallbackToken
	}
	return t, nil
}

// tokenEncoding decodes strictly, so a token has one spelling: unused bits
// in a final character are refused instead of ignored.
var tokenEncoding = base64.RawURLEncoding.Strict()

// callbackTokenMaxBytes bounds a token before any decoding.
const callbackTokenMaxBytes = 1024

func (s *CallbackSigner) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(callbackTokenPrefix + "." + payload))
	return m.Sum(nil)
}

// ValidCallbackID reports whether id is a thread or message ID: 1 to max
// bytes of ASCII letters, digits, '.', '_', ':' and '-'.
func ValidCallbackID(id string, max int) bool {
	if id == "" || len(id) > max {
		return false
	}
	for _, c := range []byte(id) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

// ValidCallbackKinds reports whether kinds are distinct callback kinds.
func ValidCallbackKinds(kinds []string) bool {
	if len(kinds) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if (k != api.AgentCallbackKindToolResult && k != api.AgentCallbackKindEvent) || seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}
