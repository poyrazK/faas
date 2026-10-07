package main

// adr: 616

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

func publicIdentityEnv(slot string) func(string) string {
	base := publicWithdrawalEnv(slot)
	return func(key string) string {
		if key == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_IDENTITY" {
			return "1"
		}
		return base(key)
	}
}

func TestPublicIdentityRequiresWithdrawalAndInstalledMechanisms(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, missing := range []string{"FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL", "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY", "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION", "FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN"} {
		s := newPublicWithdrawalFixture()
		p := ingressTestProxy(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), s)
		base := publicIdentityEnv(uuid.NewString())
		getenv := func(key string) string {
			if key == missing {
				return ""
			}
			return base(key)
		}
		if _, err := prepareRuntimePublicEdgeObserver(t.Context(), p, s, runtimePublicEdgeConfig{}, getenv, log); err == nil {
			t.Fatal("identity enabled without dependency", missing)
		}
	}
	// The identity endpoint is an additive default-off behavior even when the
	// withdrawal mechanism is explicitly enabled.
	s := newPublicWithdrawalFixture()
	p := ingressTestProxy(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), s)
	o, err := prepareRuntimePublicEdgeObserver(t.Context(), p, s, runtimePublicEdgeConfig{}, publicWithdrawalEnv(uuid.NewString()), log)
	if err != nil || o.identityHandler() != nil {
		t.Fatal("default-off endpoint installed", err)
	}
	var absent *runtimePublicEdgeObserver
	if absent.identityHandler() != nil {
		t.Fatal("default-off nil observer installed identity")
	}
}

func TestPublicIdentityUsesActualPublicListenerAndLeavesControlAndCustomerPaths(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "H2C"}[h2], func(t *testing.T) {
			s := newPublicWithdrawalFixture()
			var customerCalls, controlCalls atomic.Int32
			customer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { customerCalls.Add(1); w.WriteHeader(204) })
			p := ingressTestProxy(t, h2, customer, s)
			getenv := publicIdentityEnv(uuid.NewString())
			config := publicEdgeConfig(internalUpstreamUnix, h2, defaultListenAddr, nil, getenv)
			if config.Protocol != "adr616/public-identity-v1" {
				t.Fatal(config.Protocol)
			}
			o, err := prepareRuntimePublicEdgeObserver(t.Context(), p, s, config, getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { controlCalls.Add(1); w.WriteHeader(204) })
			publicSrv, controlSrv := buildServers("127.0.0.1:0", "127.0.0.1:0", customer, mux)
			publicSrv.Handler = ingress.WrapPublicIdentity(publicSrv.Handler, o.identityHandler())
			public := httptest.NewServer(publicSrv.Handler)
			defer public.Close()
			control := httptest.NewServer(controlSrv.Handler)
			defer control.Close()
			id := ingress.PublicEdgeIdentity{SlotID: o.member.SlotID, SessionID: o.member.SessionID, ConfigSHA256: o.member.ConfigSHA256}
			// Identity is evidence of a listener, including one that has closed
			// customer admission; probing cannot reopen or advance that fence.
			intent := uuid.NewString()
			before, err := o.activity.Withdraw(intent)
			if err != nil {
				t.Fatal(err)
			}
			if err := ingress.ProbePublicEdge(t.Context(), public.Listener.Addr().String(), ingressTestToken, id); err != nil || customerCalls.Load() != 0 {
				t.Fatal("identity not on actual public handler", err, customerCalls.Load())
			}
			after, err := o.activity.Withdraw(intent)
			if err != nil || after != before {
				t.Fatal("identity probe changed admission fence", after, before, err)
			}
			if err := ingress.ProbePublicEdge(t.Context(), control.Listener.Addr().String(), ingressTestToken, id); err == nil || controlCalls.Load() != 1 {
				t.Fatal("control listener supplied public identity", err, controlCalls.Load())
			}
			r := httptest.NewRequest(http.MethodGet, "http://app.example"+ingress.PublicIdentityPath, nil)
			w := httptest.NewRecorder()
			publicSrv.Handler.ServeHTTP(w, r)
			if w.Code != 204 || customerCalls.Load() != 1 {
				t.Fatal("customer path was intercepted", w.Code, customerCalls.Load())
			}
			// Protocol changes require a new startup configuration digest; a
			// signed proof must never accept the old protocol's reviewed tuple.
			id.ConfigSHA256 = strings.Repeat("b", 64)
			if err := ingress.ProbePublicEdge(t.Context(), public.Listener.Addr().String(), ingressTestToken, id); err == nil {
				t.Fatal("mismatched protocol digest accepted")
			}
		})
	}
}
