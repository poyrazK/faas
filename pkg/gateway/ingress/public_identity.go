package ingress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	PublicIdentityHost = "gatewayd-public.faas"
	PublicIdentityPath = "/v1/internal/runtime-public-edge/identity"
)

var ErrPublicEdgeUnverified = errors.New("private public edge binding unverified")

// PublicEdgeIdentity identifies a startup session on the actual public
// listener (ADR-702). It is neither membership nor a drain/fencing receipt.
type PublicEdgeIdentity struct {
	SlotID       string `json:"slot_id"`
	SessionID    string `json:"session_id"`
	ConfigSHA256 string `json:"config_sha256"`
	Nonce        string `json:"nonce"`
	Proof        string `json:"proof"`
}

func publicRequestProof(token, nonce string) string {
	return proof(token, "gregale/runtime-public-edge/request/v1:"+nonce)
}

func publicResponseProof(token string, id PublicEdgeIdentity) string {
	return proof(token, "gregale/runtime-public-edge/response/v1:"+id.Nonce+":"+id.SlotID+":"+id.SessionID+":"+id.ConfigSHA256)
}

func ValidatePublicEdgeIdentity(id PublicEdgeIdentity) error {
	digest, err := hex.DecodeString(id.ConfigSHA256)
	if !canonicalID(id.SlotID) || !canonicalID(id.SessionID) || err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != id.ConfigSHA256 {
		return fmt.Errorf("public edge identity requires canonical slot, session and configuration digest")
	}
	return nil
}

// NewPublicIdentityHandler must be installed only after the public process has
// installed the guard, activity tracker and withdrawal repair. Caller owns
// that wiring; this handler grants no authorization to customer requests.
func NewPublicIdentityHandler(token string, id PublicEdgeIdentity) (http.Handler, error) {
	if err := ValidateToken(token); err != nil {
		return nil, err
	}
	if err := ValidatePublicEdgeIdentity(id); err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validPublicIdentityRequest(r, token) {
			http.NotFound(w, r)
			return
		}
		response := id
		response.Nonce = r.Header.Get(NonceHeader)
		response.Proof = publicResponseProof(token, response)
		body, err := json.Marshal(response)
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

func validPublicIdentityRequest(r *http.Request, token string) bool {
	nonce := r.Header.Get(NonceHeader)
	return r.Method == http.MethodGet && r.Host == PublicIdentityHost && r.URL.Path == PublicIdentityPath && r.URL.RawPath == "" && r.URL.RawQuery == "" && !r.URL.ForceQuery && r.ContentLength == 0 && len(r.TransferEncoding) == 0 &&
		r.Header.Get("Upgrade") == "" && len(r.Header.Values(NonceHeader)) == 1 && canonicalID(nonce) && len(r.Header.Values(TokenHeader)) == 1 &&
		subtle.ConstantTimeCompare([]byte(r.Header.Get(TokenHeader)), []byte(publicRequestProof(token, nonce))) == 1
}

// WrapPublicIdentity reserves only this infrastructure host and path. Customer
// paths on other hosts continue to the original handler, including this path.
func WrapPublicIdentity(next, identity http.Handler) http.Handler {
	if identity == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == PublicIdentityHost && r.URL.Path == PublicIdentityPath {
			identity.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PublicEdgeAddress accepts literal canonical loopback TCP addresses only.
// Collection must run beside Caddy; DNS, Unix sockets, placeholders and remote
// origins need separate reviewed adapters rather than an implicit resolver.
func PublicEdgeAddress(address string) bool {
	a, err := netip.ParseAddrPort(address)
	return err == nil && a.Port() != 0 && a.Addr().IsLoopback() && !a.Addr().Is4In6() && a.Addr().Zone() == "" && a.String() == address
}

// ProbePublicEdge opens a new direct connection to this explicit backend, with
// no proxy, redirect, pooling, retry, customer forward or membership cache.
func ProbePublicEdge(ctx context.Context, address, token string, expected PublicEdgeIdentity) error {
	if !PublicEdgeAddress(address) || ValidateToken(token) != nil || ValidatePublicEdgeIdentity(expected) != nil {
		return ErrPublicEdgeUnverified
	}
	probe, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeIngressProbeTimeout)
	defer cancel()
	t := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: int64(api.DefaultMaxHeaderBytes)}
	defer t.CloseIdleConnections()
	r, err := http.NewRequestWithContext(probe, http.MethodGet, "http://"+address+PublicIdentityPath, nil)
	if err != nil {
		return ErrPublicEdgeUnverified
	}
	r.Host = PublicIdentityHost
	nonce := uuid.NewString()
	r.Header.Set(NonceHeader, nonce)
	r.Header.Set(TokenHeader, publicRequestProof(token, nonce))
	resp, err := t.RoundTrip(r)
	if err != nil {
		return fmt.Errorf("%w: public identity connection: %w", ErrPublicEdgeUnverified, err)
	}
	defer func() { _ = resp.Body.Close() }()
	id, err := readPublicIdentity(resp, nonce, token)
	if err != nil || id.SlotID != expected.SlotID || id.SessionID != expected.SessionID || id.ConfigSHA256 != expected.ConfigSHA256 || probe.Err() != nil {
		return ErrPublicEdgeUnverified
	}
	return nil
}

func readPublicIdentity(resp *http.Response, nonce, token string) (PublicEdgeIdentity, error) {
	if resp.StatusCode != http.StatusOK || resp.ContentLength < 1 || resp.ContentLength > api.RuntimeUpgradeIngressIdentityMaxBytes || len(resp.TransferEncoding) != 0 {
		return PublicEdgeIdentity{}, ErrPublicEdgeUnverified
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.RuntimeUpgradeIngressIdentityMaxBytes+1))
	if err != nil || int64(len(body)) != resp.ContentLength {
		return PublicEdgeIdentity{}, ErrPublicEdgeUnverified
	}
	var id PublicEdgeIdentity
	if err := DecodeUniqueJSON(body, &id); err != nil || ValidatePublicEdgeIdentity(id) != nil || id.Nonce != nonce ||
		subtle.ConstantTimeCompare([]byte(id.Proof), []byte(publicResponseProof(token, id))) != 1 {
		return PublicEdgeIdentity{}, ErrPublicEdgeUnverified
	}
	return id, nil
}

// DecodeUniqueJSON rejects unknown fields, trailing values and duplicate keys
// at every depth. Its callers must bound input bytes before decoding.
func DecodeUniqueJSON(body []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	if err := uniqueJSONValue(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func uniqueJSONValue(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	keys := make(map[string]bool)
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			name = strings.ToLower(name)
			if !ok || keys[name] {
				return fmt.Errorf("duplicate or invalid JSON key")
			}
			keys[name] = true
		}
		if err := uniqueJSONValue(d); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
