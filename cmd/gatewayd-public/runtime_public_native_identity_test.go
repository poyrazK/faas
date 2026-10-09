package main

// adr: 710

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

func publicNativeIdentityEnv(slot string) func(string) string {
	base := publicIdentityEnv(slot)
	return func(key string) string {
		if key == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_NATIVE_IDENTITY" {
			return "1"
		}
		return base(key)
	}
}

func TestPublicNativeIdentityRequiresEntireGuardChainAndChangesConfigFingerprint(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, missing := range []string{"FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_IDENTITY", "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL", "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY", "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION", "FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN"} {
		s := newPublicWithdrawalFixture()
		p := ingressTestProxy(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), s)
		base := publicNativeIdentityEnv(uuid.NewString())
		getenv := func(key string) string {
			if key == missing {
				return ""
			}
			return base(key)
		}
		if o, err := prepareRuntimePublicEdgeObserver(t.Context(), p, s, runtimePublicEdgeConfig{}, getenv, log); err == nil || o != nil {
			t.Fatal("native identity enabled without prerequisite", missing, o, err)
		}
	}
	slot := uuid.NewString()
	legacy := publicEdgeConfig(internalUpstreamUnix, false, defaultListenAddr, nil, publicIdentityEnv(slot))
	native := publicEdgeConfig(internalUpstreamUnix, false, defaultListenAddr, nil, publicNativeIdentityEnv(slot))
	a, _ := json.Marshal(legacy)
	b, _ := json.Marshal(native)
	if legacy.Protocol != "adr616/public-identity-v1" || native.Protocol != "adr624/native-startup-v1" || sha256.Sum256(a) == sha256.Sum256(b) {
		t.Fatal("native proof did not version the selected startup configuration")
	}
	s := newPublicWithdrawalFixture()
	p := ingressTestProxy(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), s)
	o, err := prepareRuntimePublicEdgeObserver(t.Context(), p, s, runtimePublicEdgeConfig{}, publicIdentityEnv(slot), log)
	if err != nil || o.nativeIdentityHandler() != nil || o.identityHandler() == nil {
		t.Fatal("default-off changed the existing identity endpoint", err)
	}
	var absent *runtimePublicEdgeObserver
	if absent.nativeIdentityHandler() != nil {
		t.Fatal("nil observer installed native endpoint")
	}
}

func TestPublicNativeIdentityFactoryCannotRetargetStartupOrInstallPartialResult(t *testing.T) {
	for _, kind := range []string{"source-error", "nil-handler", "slot", "session", "config", "invalid-epoch", "cancel", "cancel-result", "valid"} {
		t.Run(kind, func(t *testing.T) {
			var logs bytes.Buffer
			s := newPublicWithdrawalFixture()
			p := ingressTestProxy(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), s)
			o, err := prepareRuntimePublicEdgeObserver(t.Context(), p, s, runtimePublicEdgeConfig{}, publicIdentityEnv(uuid.NewString()), slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			var called bool
			factory := func(ctx context.Context, token string, id ingress.PublicEdgeIdentity) (http.Handler, ingress.NativePublicStartup, error) {
				called = true
				if token != ingressTestToken || id.SlotID != o.member.SlotID || id.SessionID != o.member.SessionID || id.ConfigSHA256 != o.member.ConfigSHA256 {
					t.Fatal("factory received invented startup or wrong private credential")
				}
				startup := ingress.NativePublicStartup{SlotID: id.SlotID, SessionID: id.SessionID, ConfigSHA256: id.ConfigSHA256, Epoch: ingress.NativeProcessEpoch{MachineID: strings.Repeat("1", 32), BootID: uuid.NewString(), PID: 1234, StartTicks: 56789, PIDNamespace: "pid:[10]", NetNamespace: "net:[20]"}}
				h := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
				switch kind {
				case "source-error":
					return h, startup, errors.New("metadata unavailable")
				case "nil-handler":
					h = nil
				case "slot":
					startup.SlotID = uuid.NewString()
				case "session":
					startup.SessionID = uuid.NewString()
				case "config":
					startup.ConfigSHA256 = strings.Repeat("b", 64)
				case "invalid-epoch":
					startup.Epoch.BootID = "bad"
				case "cancel":
					return nil, ingress.NativePublicStartup{}, ctx.Err()
				}
				return h, startup, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "cancel" || kind == "cancel-result" {
				cancel()
			}
			err = o.configureNativeIdentity(ctx, ingressTestToken, factory)
			if !called {
				t.Fatal("factory was not called")
			}
			if kind != "valid" {
				if !errors.Is(err, ingress.ErrNativeStartupUnverified) || o.nativeIdentityHandler() != nil || strings.Contains(logs.String(), "machine_id") {
					t.Fatal("failure installed or logged partial identity", err, logs.String())
				}
				return
			}
			if err != nil || o.nativeIdentityHandler() == nil || !strings.Contains(logs.String(), "boot_id") || strings.Contains(logs.String(), ingressTestToken) {
				t.Fatal("valid native startup not installed or credential exposed", err, logs.String())
			}
			customer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(418) })
			public := ingress.WrapNativePublicIdentity(customer, o.nativeIdentityHandler())
			for _, host := range []string{ingress.PublicIdentityHost, "app.example"} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodGet, "http://"+host+ingress.NativePublicIdentityPath, nil)
				public.ServeHTTP(w, r)
				want := 204
				if host != ingress.PublicIdentityHost {
					want = 418
				}
				if w.Code != want {
					t.Fatal("selected handler routing mismatch", host, w.Code)
				}
			}
		})
	}
}
