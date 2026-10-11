package ingress

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const NativePublicIdentityPath = "/v1/internal/runtime-public-edge/native-startup"

var ErrNativeStartupUnverified = errors.New("native public startup binding unverified")

// NativeProcessEpoch identifies the startup's kernel-visible host/process
// context. It does not authenticate physical hardware, external resource IDs,
// exclusive socket ownership, or elimination of snapshot/resume paths.
type NativeProcessEpoch struct {
	MachineID    string `json:"machine_id"`
	BootID       string `json:"boot_id"`
	PID          int    `json:"pid"`
	StartTicks   uint64 `json:"start_ticks"`
	PIDNamespace string `json:"pid_namespace"`
	NetNamespace string `json:"net_namespace"`
}

type NativePublicStartup struct {
	SlotID       string             `json:"slot_id"`
	SessionID    string             `json:"session_id"`
	ConfigSHA256 string             `json:"config_sha256"`
	Epoch        NativeProcessEpoch `json:"epoch"`
}

type nativePublicEnvelope struct {
	Startup NativePublicStartup `json:"startup"`
	Nonce   string              `json:"nonce"`
	Proof   string              `json:"proof"`
}

// NativeStartupProof is a nonce-authenticated observation, not a termination
// receipt, historical enrollment record or externally qualified host binding.
type NativeStartupProof struct {
	startup  NativePublicStartup
	envelope []byte
}

func (p NativeStartupProof) Startup() NativePublicStartup { return p.startup }
func (p NativeStartupProof) Envelope() []byte             { return slices.Clone(p.envelope) }

func ValidateNativePublicStartup(startup NativePublicStartup) error {
	if ValidatePublicEdgeIdentity(PublicEdgeIdentity{SlotID: startup.SlotID, SessionID: startup.SessionID, ConfigSHA256: startup.ConfigSHA256}) != nil || validateNativeProcessEpoch(startup.Epoch) != nil {
		return ErrNativeStartupUnverified
	}
	return nil
}

func validateNativeProcessEpoch(e NativeProcessEpoch) error {
	if len(e.MachineID) != 32 || e.MachineID == strings.Repeat("0", 32) || strings.IndexFunc(e.MachineID, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) != -1 || !canonicalID(e.BootID) || e.PID < 2 || e.StartTicks == 0 || !nativeEpochNamespace(e.PIDNamespace, "pid") || !nativeEpochNamespace(e.NetNamespace, "net") {
		return ErrNativeStartupUnverified
	}
	if _, err := strconv.ParseInt(strconv.Itoa(e.PID), 10, 32); err != nil {
		return ErrNativeStartupUnverified
	}
	return nil
}

func nativeEpochNamespace(value, kind string) bool {
	if !strings.HasPrefix(value, kind+":[") || !strings.HasSuffix(value, "]") {
		return false
	}
	text := value[len(kind)+2 : len(value)-1]
	n, err := strconv.ParseUint(text, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == text
}

func nativePublicRequestProof(token, nonce string) string {
	return proof(token, "gregale/runtime-public-edge/native-startup/request/v1:"+nonce)
}

func nativePublicResponseProof(token string, envelope nativePublicEnvelope) string {
	envelope.Proof = ""
	raw, err := json.Marshal(envelope)
	if err != nil {
		return ""
	}
	return proof(token, "gregale/runtime-public-edge/native-startup/response/v1:"+string(raw))
}

// NewNativePublicIdentityHandler captures its own real Linux epoch exactly once
// before serving. No caller-supplied host/PID DTO or filesystem path is accepted.
// Caller must install the existing guard, activity and withdrawal mechanisms.
func NewNativePublicIdentityHandler(ctx context.Context, token string, id PublicEdgeIdentity) (http.Handler, NativePublicStartup, error) {
	if ctx == nil || ValidateToken(token) != nil || ValidatePublicEdgeIdentity(id) != nil {
		return nil, NativePublicStartup{}, ErrNativeStartupUnverified
	}
	epoch, err := captureNativeProcessEpoch(ctx)
	if err != nil {
		return nil, NativePublicStartup{}, ErrNativeStartupUnverified
	}
	startup := NativePublicStartup{SlotID: id.SlotID, SessionID: id.SessionID, ConfigSHA256: id.ConfigSHA256, Epoch: epoch}
	h, err := newNativePublicIdentityHandler(token, startup)
	if err != nil || ctx.Err() != nil {
		return nil, NativePublicStartup{}, ErrNativeStartupUnverified
	}
	return h, startup, nil
}

func newNativePublicIdentityHandler(token string, startup NativePublicStartup) (http.Handler, error) {
	if ValidateToken(token) != nil || ValidateNativePublicStartup(startup) != nil {
		return nil, ErrNativeStartupUnverified
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validNativePublicRequest(r, token) {
			http.NotFound(w, r)
			return
		}
		response := nativePublicEnvelope{Startup: startup, Nonce: r.Header.Get(NonceHeader)}
		response.Proof = nativePublicResponseProof(token, response)
		body, err := json.Marshal(response)
		if err != nil || len(body) > api.RuntimeUpgradeIngressIdentityMaxBytes {
			http.Error(w, "identity unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}), nil
}

func validNativePublicRequest(r *http.Request, token string) bool {
	nonce := r.Header.Get(NonceHeader)
	return r.Method == http.MethodGet && r.Host == PublicIdentityHost && r.URL.Path == NativePublicIdentityPath && r.URL.RawPath == "" && r.URL.RawQuery == "" && !r.URL.ForceQuery && r.ContentLength == 0 && len(r.TransferEncoding) == 0 && len(r.Header.Values("Upgrade")) == 0 && len(r.Header.Values(NonceHeader)) == 1 && canonicalID(nonce) && len(r.Header.Values(TokenHeader)) == 1 && subtle.ConstantTimeCompare([]byte(r.Header.Get(TokenHeader)), []byte(nativePublicRequestProof(token, nonce))) == 1
}

func WrapNativePublicIdentity(next, identity http.Handler) http.Handler {
	if identity == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == PublicIdentityHost && r.URL.Path == NativePublicIdentityPath {
			identity.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ProbeNativePublicStartup authenticates the complete expected startup and
// epoch on one new selected loopback connection. It never discovers endpoints,
// refreshes membership or proves a future lease, hardware identity or fencing.
func ProbeNativePublicStartup(ctx context.Context, address, token string, expected NativePublicStartup) (NativeStartupProof, error) {
	if ctx == nil || !PublicEdgeAddress(address) || ValidateToken(token) != nil || ValidateNativePublicStartup(expected) != nil {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	probe, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeIngressProbeTimeout)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: int64(api.DefaultMaxHeaderBytes)}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(probe, http.MethodGet, "http://"+address+NativePublicIdentityPath, nil)
	if err != nil {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	nonce := uuid.NewString()
	request.Host = PublicIdentityHost
	request.Header.Set(NonceHeader, nonce)
	request.Header.Set(TokenHeader, nativePublicRequestProof(token, nonce))
	response, err := transport.RoundTrip(request)
	if err != nil {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	defer func() { _ = response.Body.Close() }()
	got, err := readNativePublicIdentity(response, token, nonce)
	if err != nil || got.Startup() != expected || probe.Err() != nil {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	return got, nil
}

func readNativePublicIdentity(response *http.Response, token, nonce string) (NativeStartupProof, error) {
	if response.StatusCode != http.StatusOK || response.ContentLength < 1 || response.ContentLength > api.RuntimeUpgradeIngressIdentityMaxBytes || len(response.TransferEncoding) != 0 || len(response.Header.Values("Content-Type")) != 1 || response.Header.Get("Content-Type") != "application/json" || len(response.Header.Values("Content-Encoding")) != 0 || response.Uncompressed {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.RuntimeUpgradeIngressIdentityMaxBytes+1))
	if err != nil || int64(len(raw)) != response.ContentLength || len(response.Trailer) != 0 {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	var envelope nativePublicEnvelope
	if json.Unmarshal(raw, &envelope) != nil || ValidateNativePublicStartup(envelope.Startup) != nil || !canonicalID(nonce) || envelope.Nonce != nonce || subtle.ConstantTimeCompare([]byte(envelope.Proof), []byte(nativePublicResponseProof(token, envelope))) != 1 {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(canonical, raw) {
		return NativeStartupProof{}, ErrNativeStartupUnverified
	}
	return NativeStartupProof{startup: envelope.Startup, envelope: slices.Clone(raw)}, nil
}
