// Package ingress binds a private public-edge forward to the internal process
// on that exact connection (ADR-612). It grants no whole-fleet or VM authority.
package ingress

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	IdentityHost = "gatewayd-internal.faas"
	IdentityPath = "/v1/internal/runtime-gateway/identity"
	TokenHeader  = "X-Gregale-Runtime-Ingress-Token"
	NonceHeader  = "X-Gregale-Runtime-Ingress-Nonce"
)

var ErrUnverified = errors.New("private internal ingress binding unverified")

type Identity struct {
	SlotID          string `json:"slot_id"`
	SessionID       string `json:"session_id"`
	Nonce           string `json:"nonce"`
	AdmissionFenced bool   `json:"admission_fenced"`
	Proof           string `json:"proof"`
}

func proof(token, message string) string {
	key, err := hex.DecodeString(token)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func requestProof(token, nonce string) string {
	return proof(token, "gregale/runtime-ingress/request/v1:"+nonce)
}

func responseProof(token string, identity Identity) string {
	return proof(token, "gregale/runtime-ingress/response/v1:"+identity.Nonce+":"+identity.SlotID+":"+identity.SessionID+":"+strconv.FormatBool(identity.AdmissionFenced))
}

func canonicalID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func ValidateToken(token string) error {
	b, err := hex.DecodeString(token)
	if err != nil || len(b) != api.RuntimeUpgradeIngressTokenBytes || hex.EncodeToString(b) != token {
		return fmt.Errorf("private ingress token must be canonical hex for %d random bytes", api.RuntimeUpgradeIngressTokenBytes)
	}
	return nil
}

// NewIdentityHandler is installed on the protected internal forwarding listener
// only after its process-wide forwarding fence tracker has been constructed.
func NewIdentityHandler(token, slotID, sessionID string) (http.Handler, error) {
	if err := ValidateToken(token); err != nil {
		return nil, err
	}
	if !canonicalID(slotID) || !canonicalID(sessionID) {
		return nil, fmt.Errorf("private ingress identity requires canonical slot and session")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Host != IdentityHost || r.URL.Path != IdentityPath || r.URL.RawQuery != "" || r.ContentLength != 0 || r.Header.Get("Upgrade") != "" ||
			len(r.Header.Values(TokenHeader)) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get(TokenHeader)), []byte(requestProof(token, r.Header.Get(NonceHeader)))) != 1 ||
			len(r.Header.Values(NonceHeader)) != 1 || !canonicalID(r.Header.Get(NonceHeader)) {
			http.NotFound(w, r)
			return
		}
		identity := Identity{SlotID: slotID, SessionID: sessionID, Nonce: r.Header.Get(NonceHeader), AdmissionFenced: true}
		identity.Proof = responseProof(token, identity)
		body, err := json.Marshal(identity)
		if err != nil {
			http.Error(w, "identity unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}), nil
}

// Wrap preserves customer-owned paths on app hosts. An infrastructure-host
// request for the private path is handled here even with invalid credentials.
func Wrap(next, identity http.Handler) http.Handler {
	if identity == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == IdentityHost && r.URL.Path == IdentityPath {
			identity.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readIdentity(resp *http.Response, nonce, token string) (Identity, error) {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 1 || resp.ContentLength > api.RuntimeUpgradeIngressIdentityMaxBytes || resp.Close {
		return Identity{}, ErrUnverified
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.RuntimeUpgradeIngressIdentityMaxBytes+1))
	if err != nil || len(body) > api.RuntimeUpgradeIngressIdentityMaxBytes {
		return Identity{}, ErrUnverified
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var identity Identity
	if err := d.Decode(&identity); err != nil || !canonicalID(identity.SlotID) || !canonicalID(identity.SessionID) || identity.Nonce != nonce || !identity.AdmissionFenced ||
		subtle.ConstantTimeCompare([]byte(identity.Proof), []byte(responseProof(token, identity))) != 1 {
		return Identity{}, ErrUnverified
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return Identity{}, ErrUnverified
	}
	return identity, nil
}
