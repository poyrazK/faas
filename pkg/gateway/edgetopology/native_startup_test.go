package edgetopology

// adr: 711

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

type startupFixture struct {
	probe   *NativeStartupProbe
	review  NativeStartupReview
	reader  *nativeFixtureReader
	startup ingress.NativePublicStartup
	opens   int
	calls   atomic.Int32
	edit    func(*ingress.NativePublicStartup)
	badMAC  bool
}

// Synthetic kernel/service metadata and protocol issuer are test-only. The
// production collector cannot accept this reader or caller-supplied epoch.
func newStartupFixture(t *testing.T) *startupFixture {
	t.Helper()
	f := &startupFixture{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		nonce := r.Header.Get(ingress.NonceHeader)
		if r.Method != http.MethodGet || r.Host != ingress.PublicIdentityHost || r.URL.Path != ingress.NativePublicIdentityPath || r.Header.Get(ingress.TokenHeader) != startupMAC(edgeToken, "gregale/runtime-public-edge/native-startup/request/v1:"+nonce) {
			t.Error("wrong native startup request")
			w.WriteHeader(404)
			return
		}
		startup := f.startup
		if f.edit != nil {
			f.edit(&startup)
		}
		envelope := struct {
			Startup ingress.NativePublicStartup `json:"startup"`
			Nonce   string                      `json:"nonce"`
			Proof   string                      `json:"proof"`
		}{Startup: startup, Nonce: nonce}
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Error(err)
		}
		envelope.Proof = startupMAC(edgeToken, "gregale/runtime-public-edge/native-startup/response/v1:"+string(raw))
		if f.badMAC {
			envelope.Proof = strings.Repeat("f", 64)
		}
		raw, err = json.Marshal(envelope)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw)
	}))
	t.Cleanup(s.Close)
	id := ingress.PublicEdgeIdentity{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)}
	scope := nativeTestScope(s.Listener.Addr().String(), "127.0.0.1:2019")
	scope.Services = scope.Services[1:]
	f.review = NativeStartupReview{Scope: scope, Binding: Binding{Address: s.Listener.Addr().String(), Edge: id}}
	_, f.startup, _ = CanonicalNativeStartupReview(f.review)
	f.reader = newNativeFixtureReader(t, scope)
	var err error
	f.probe, err = NewNativeStartupProbe(edgeToken)
	if err != nil {
		t.Fatal(err)
	}
	f.probe.open = func(ctx context.Context, _ NativeScopeReview) (nativeScopeSession, error) {
		f.opens++
		if ctx.Err() != nil {
			return nil, ErrNativeUnverified
		}
		return f.reader, nil
	}
	return f
}

func startupMAC(token, text string) string {
	key, _ := hex.DecodeString(token)
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(text))
	return hex.EncodeToString(m.Sum(nil))
}

func TestNativeStartupJoinsAuthenticatedEpochWithTwoRetainedScopeBookends(t *testing.T) {
	f := newStartupFixture(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	before, _ := json.Marshal(f.review)
	var previous []byte
	for i := 1; i <= 2; i++ {
		got, err := f.probe.Observe(t.Context(), f.review)
		if err != nil || got.Startup() != f.startup || ValidateNativeStartupRecord(got.Envelope(), f.review) != nil {
			t.Fatal(got, err)
		}
		if f.opens != i || f.reader.captures != 2*i || f.reader.closes != i || f.calls.Load() != int32(i) || bytes.Equal(previous, got.Envelope()) {
			t.Fatal("cached, unretained or incomplete observation", f.opens, f.reader.captures, f.reader.closes, f.calls.Load())
		}
		previous = got.Envelope()
		copyOut := got.Envelope()
		copyOut[0] = '!'
		if !bytes.Equal(got.Envelope(), previous) {
			t.Fatal("caller mutated retained evidence")
		}
		for _, secret := range []string{edgeToken, "execution_available", "valid_until", "secret-arbitrary-native-error"} {
			if bytes.Contains(previous, []byte(secret)) {
				t.Fatal("secret or broader authority in observation", secret)
			}
		}
	}
	after, _ := json.Marshal(f.review)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated administrative selection")
	}
}

func TestNativeStartupRejectsEveryForeignAuthenticatedStartupField(t *testing.T) {
	for _, kind := range []string{"slot", "session", "config", "machine", "boot", "pid", "start", "pidns", "netns", "hmac"} {
		t.Run(kind, func(t *testing.T) {
			f := newStartupFixture(t)
			f.badMAC = kind == "hmac"
			f.edit = func(s *ingress.NativePublicStartup) {
				switch kind {
				case "slot":
					s.SlotID = uuid.NewString()
				case "session":
					s.SessionID = uuid.NewString()
				case "config":
					s.ConfigSHA256 = strings.Repeat("b", 64)
				case "machine":
					s.Epoch.MachineID = strings.Repeat("2", 32)
				case "boot":
					s.Epoch.BootID = uuid.NewString()
				case "pid":
					s.Epoch.PID++
				case "start":
					s.Epoch.StartTicks++
				case "pidns":
					s.Epoch.PIDNamespace = "pid:[99]"
				case "netns":
					s.Epoch.NetNamespace = "net:[99]"
				}
			}
			got, err := f.probe.Observe(t.Context(), f.review)
			if !errors.Is(err, ErrNativeUnverified) || len(got.Envelope()) != 0 || f.reader.captures != 1 || f.reader.closes != 1 {
				t.Fatal("foreign proof or partial scope retained", got, err)
			}
		})
	}
}

func TestNativeStartupRejectsNativeReplacementAcrossPublicProbe(t *testing.T) {
	for _, kind := range []string{"boot", "start", "namespace", "invocation", "executable", "cgroup", "socket", "fd", "exit", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := newStartupFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.reader.captureHook = func(call int) {
				if call != 2 {
					return
				}
				service := f.review.Scope.Services[0]
				base := strconv.Itoa(service.PID) + "/"
				switch kind {
				case "boot":
					f.reader.reads["sys/kernel/random/boot_id"] = []byte(uuid.NewString() + "\n")
				case "start":
					service.StartTicks++
					f.reader.reads[base+"stat"] = nativeStatBody(service)
				case "namespace":
					f.reader.links[base+"ns/net"] = "net:[99]"
				case "invocation":
					service.InvocationID = strings.Repeat("4", 32)
					f.reader.units[service.Unit] = nativeUnitBody(service)
				case "executable":
					id := f.reader.executables[base+"exe"]
					id.Inode++
					f.reader.executables[base+"exe"] = id
				case "cgroup":
					id := f.reader.cgroups[service.Cgroup]
					id.Device++
					f.reader.cgroups[service.Cgroup] = id
				case "socket":
					f.reader.reads["self/net/tcp"] = bytes.ReplaceAll(f.reader.reads["self/net/tcp"], []byte("1000 "), []byte("9999 "))
					f.reader.links[base+"fd/3"] = "socket:[9999]"
				case "fd":
					f.reader.entries[base+"fd"] = append(f.reader.entries[base+"fd"], "10")
					f.reader.links[base+"fd/10"] = "socket:[1000]"
				case "exit":
					f.reader.fail = "alive"
				case "cancel":
					cancel()
				}
			}
			got, err := f.probe.Observe(ctx, f.review)
			if !errors.Is(err, ErrNativeUnverified) || len(got.Envelope()) != 0 || strings.Contains(err.Error(), "secret-arbitrary-native-error") || f.reader.closes != 1 {
				t.Fatal("replacement emitted enrollment evidence", got, err)
			}
		})
	}
}

func TestNativeStartupReviewIsOwnedAndRejectsImplicitOrMultipleServiceSelection(t *testing.T) {
	for _, kind := range []string{"multiple", "address", "unheld", "nonce", "proof", "pid-overflow", "missing-host", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := newStartupFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "multiple":
				f.review.Scope.Services = nativeTestScope(f.review.Binding.Address, "127.0.0.1:2019").Services
			case "address":
				f.review.Binding.Address = "localhost:8080"
			case "unheld":
				f.review.Binding.Address = "127.0.0.1:8083"
			case "nonce":
				f.review.Binding.Edge.Nonce = uuid.NewString()
			case "proof":
				f.review.Binding.Edge.Proof = strings.Repeat("a", 64)
			case "pid-overflow":
				f.review.Scope.Services[0].PID = 1 << 31
			case "missing-host":
				f.review.Scope.Host.Addresses = nil
			case "cancel":
				cancel()
			}
			got, err := f.probe.Observe(ctx, f.review)
			if !errors.Is(err, ErrNativeUnverified) || len(got.Envelope()) != 0 || f.opens != 0 || f.calls.Load() != 0 {
				t.Fatal("invalid review performed I/O", got, err)
			}
		})
	}
	f := newStartupFixture(t)
	want := f.startup
	f.reader.captureHook = func(call int) {
		if call == 1 {
			f.review.Scope.Host.Addresses[0].IP = "192.0.2.99"
			f.review.Scope.Services[0].TCPListeners[0] = "127.0.0.1:9999"
		}
	}
	got, err := f.probe.Observe(t.Context(), f.review)
	if err != nil || got.Startup() != want {
		t.Fatal("retained caller-owned slices", got, err)
	}
	if p, err := NewNativeStartupProbe("bad"); p != nil || !errors.Is(err, ErrNativeUnverified) {
		t.Fatal(p, err)
	}
	var absent *NativeStartupProbe
	if got, err := absent.Observe(t.Context(), f.review); !errors.Is(err, ErrNativeUnverified) || len(got.Envelope()) != 0 {
		t.Fatal(got, err)
	}
}

func TestNativeStartupRecordRejectsAmbiguousIncompleteOrDifferentSelectedEvidence(t *testing.T) {
	f := newStartupFixture(t)
	got, err := f.probe.Observe(t.Context(), f.review)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"bookend", "late-inode", "scope", "service", "listener", "empty-fds", "duplicate-fds", "unordered-fds", "proof", "time", "unknown", "trailing", "bound"} {
		t.Run(kind, func(t *testing.T) {
			var record NativeStartupSnapshot
			if err := json.Unmarshal(got.Envelope(), &record); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "bookend":
				record.Native = record.Native[:1]
			case "late-inode":
				record.Native[1].Services[0].Executable.Inode++
			case "scope":
				record.Native[0].Host.BootID = uuid.NewString()
			case "service":
				record.Native[0].Services[0].Review.UID++
			case "listener":
				record.Native[0].Services[0].Listeners[0].Address = "127.0.0.1:9999"
			case "empty-fds":
				record.Native[0].Services[0].Listeners[0].FDs = nil
				record.Native[1].Services[0].Listeners[0].FDs = nil
			case "duplicate-fds":
				for i := range record.Native {
					record.Native[i].Services[0].Listeners[0].FDs = []int{3, 3}
				}
			case "unordered-fds":
				for i := range record.Native {
					record.Native[i].Services[0].Listeners[0].FDs = []int{10, 3}
				}
			case "proof":
				record.Proof = nil
			case "time":
				record.CheckedAt = record.Native[0].CheckedAt.Add(-api.RuntimeUpgradeNativeStartupTimeout)
			}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unknown":
				raw = append([]byte(`{"other":true,`), raw[1:]...)
			case "trailing":
				raw = append(raw, '\n')
			case "bound":
				raw = bytes.Repeat([]byte("a"), api.RuntimeUpgradeNativeStartupEvidenceMaxBytes+1)
			}
			if err := ValidateNativeStartupRecord(raw, f.review); !errors.Is(err, ErrNativeUnverified) {
				t.Fatal("invalid stored shape accepted", kind, err)
			}
		})
	}
}

func TestNativeStartupStructuralValidationDoesNotAuthenticateOrRehydrateProof(t *testing.T) {
	f := newStartupFixture(t)
	got, err := f.probe.Observe(t.Context(), f.review)
	if err != nil {
		t.Fatal(err)
	}
	var record NativeStartupSnapshot
	if err := json.Unmarshal(got.Envelope(), &record); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Startup ingress.NativePublicStartup `json:"startup"`
		Nonce   string                      `json:"nonce"`
		Proof   string                      `json:"proof"`
	}
	if err := json.Unmarshal(record.Proof, &stored); err != nil {
		t.Fatal(err)
	}
	stored.Proof = strings.Repeat("f", 64)
	record.Proof, _ = json.Marshal(stored)
	raw, _ := json.Marshal(record)
	if err := ValidateNativeStartupRecord(raw, f.review); err != nil {
		t.Fatal("structural validation claimed to check unavailable HMAC key", err)
	}
	f.badMAC = true
	if forged, err := f.probe.Observe(t.Context(), f.review); !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(forged, NativeStartupObservation{}) {
		t.Fatal("forged HMAC produced an opaque observation", forged, err)
	}
}
