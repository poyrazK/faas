package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestEnvOr_EmptyFallback pins the envOr semantics (empty env
// falls back to def). The default for FAAS_PUBLIC_LISTEN_ADDR is
// 127.0.0.1:8080 in plain-HTTP mode (was :443 in TLS mode).
func TestEnvOr_EmptyFallback(t *testing.T) {
	t.Setenv("FAAS_PUBLIC_LISTEN_ADDR", "")
	got := envOr("FAAS_PUBLIC_LISTEN_ADDR", "127.0.0.1:8080")
	if got != "127.0.0.1:8080" {
		t.Errorf("envOr empty = %q, want 127.0.0.1:8080", got)
	}
	t.Setenv("FAAS_PUBLIC_LISTEN_ADDR", "127.0.0.1:18443")
	got = envOr("FAAS_PUBLIC_LISTEN_ADDR", "127.0.0.1:8080")
	if got != "127.0.0.1:18443" {
		t.Errorf("envOr set = %q, want 127.0.0.1:18443", got)
	}
}

// TestHstsEnabledFromEnv_LookupEnv pins the os.LookupEnv path
// (per the FAAS_APID_METRICS_ADDR empty=skip precedent). An
// explicit empty value must be distinguishable from unset.
func TestHstsEnabledFromEnv_LookupEnv(t *testing.T) {
	t.Setenv("FAAS_HSTS_ENABLED", "")
	if v := hstsEnabledFromEnv("FAAS_HSTS_ENABLED"); v != "" {
		t.Errorf("hstsEnabledFromEnv explicit-empty = %q, want empty string", v)
	}
	t.Setenv("FAAS_HSTS_ENABLED", "true")
	if v := hstsEnabledFromEnv("FAAS_HSTS_ENABLED"); v != "true" {
		t.Errorf("hstsEnabledFromEnv set = %q, want true", v)
	}
	// unset
	if err := os.Unsetenv("FAAS_HSTS_ENABLED"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	if v := hstsEnabledFromEnv("FAAS_HSTS_ENABLED"); v != "" {
		t.Errorf("hstsEnabledFromEnv unset = %q, want empty string", v)
	}
}

// TestDefaultPublicControlAddr_ADR070 pins the loopback control
// listener default at :9092 per ADR-070 (Tier A7 edge split). The
// legacy gatewayd daemon binds :9090 on the same node; a default
// drift here would silently collide and crash-loop both daemons on
// a non-systemd bring-up.
func TestDefaultPublicControlAddr_ADR070(t *testing.T) {
	if defaultPublicControlAddr != "127.0.0.1:9092" {
		t.Errorf("defaultPublicControlAddr = %q, want 127.0.0.1:9092 (ADR-070)", defaultPublicControlAddr)
	}
}

// TestBuildServers_PinsMaxHeaderBytes asserts both servers expose
// MaxHeaderBytes = api.DefaultMaxHeaderBytes. A future stdlib default
// change cannot widen the attack surface on this listener.
func TestBuildServers_PinsMaxHeaderBytes(t *testing.T) {
	pub, ctrl := buildServers("127.0.0.1:8080", "127.0.0.1:9092", http.NotFoundHandler(), http.NewServeMux())
	if pub.MaxHeaderBytes != api.DefaultMaxHeaderBytes {
		t.Errorf("public MaxHeaderBytes = %d, want %d", pub.MaxHeaderBytes, api.DefaultMaxHeaderBytes)
	}
	if ctrl.MaxHeaderBytes != api.DefaultMaxHeaderBytes {
		t.Errorf("control MaxHeaderBytes = %d, want %d", ctrl.MaxHeaderBytes, api.DefaultMaxHeaderBytes)
	}
}

// TestBuildServers_PublicListenerPinsKnobs — the customer-facing
// edge installs the canonical customer-facing knob set (ADR-122
// post-merge audit, issue #995 closure): RHT=10s + RT=60s + WT=300s
// + IT=120s (matches apid's customer-facing listener at
// cmd/apid/main.go:452 via APIDIdleTimeoutSecondsDefault=120) +
// MHB=1 MiB. The pre-amendment listener had IdleTimeout=0 (unlimited
// keep-alive); this test pins the new value so a regression is loud.
func TestBuildServers_PublicListenerPinsKnobs(t *testing.T) {
	pub, _ := buildServers("127.0.0.1:8080", "127.0.0.1:9092", http.NotFoundHandler(), http.NewServeMux())
	if pub.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("public RHT = %v, want 10s", pub.ReadHeaderTimeout)
	}
	if pub.ReadTimeout != 60*time.Second {
		t.Errorf("public RT = %v, want 60s", pub.ReadTimeout)
	}
	if pub.WriteTimeout != 300*time.Second {
		t.Errorf("public WT = %v, want 300s", pub.WriteTimeout)
	}
	if pub.IdleTimeout != 120*time.Second {
		t.Errorf("public IT = %v, want 120s (matches apid customer-facing listener)", pub.IdleTimeout)
	}
}

// TestBuildServers_ControlMuxAdoptsMetricsVariant — the loopback
// :9092 control mux installs the canonical metrics variant (ADR-122):
// RHT=10s + RT=10s + WT=10s + IT=60s + MHB=1 MiB. The pre-amendment
// listener had only RHT=5s + MHB; the four missing knobs are the
// audit's Site 2 closure.
func TestBuildServers_ControlMuxAdoptsMetricsVariant(t *testing.T) {
	_, ctrl := buildServers("127.0.0.1:8080", "127.0.0.1:9092", http.NotFoundHandler(), http.NewServeMux())
	if ctrl.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("control RHT = %v, want 10s", ctrl.ReadHeaderTimeout)
	}
	if got, want := ctrl.ReadTimeout, time.Duration(api.MetricsReadTimeoutSecondsDefault)*time.Second; got != want {
		t.Errorf("control RT = %v, want %v", got, want)
	}
	if got, want := ctrl.WriteTimeout, time.Duration(api.MetricsWriteTimeoutSecondsDefault)*time.Second; got != want {
		t.Errorf("control WT = %v, want %v", got, want)
	}
	if got, want := ctrl.IdleTimeout, time.Duration(api.MetricsIdleTimeoutSecondsDefault)*time.Second; got != want {
		t.Errorf("control IT = %v, want %v", got, want)
	}
	if ctrl.MaxHeaderBytes != api.DefaultMaxHeaderBytes {
		t.Errorf("control MHB = %d, want %d", ctrl.MaxHeaderBytes, api.DefaultMaxHeaderBytes)
	}
}

// TestRequirePublicBindInMultiHost_AcceptsLoopbackDefaultInSingleBox
// pins the single-box escape: FAAS_NODE_NAME unset + loopback
// defaults must pass. Without this, single-box dev installs
// (the most common case) would refuse to boot. The check must
// only fire when the operator has explicitly opted into the
// multi-box posture by setting FAAS_NODE_NAME.
func TestRequirePublicBindInMultiHost_AcceptsLoopbackDefaultInSingleBox(t *testing.T) {
	// Unset FAAS_NODE_NAME (default single-box posture) and
	// both listen addrs. t.Setenv to "" sets the variable to
	// empty (os.LookupEnv returns ok=true), which is NOT the
	// "unset" case our gate checks; we use os.Unsetenv.
	os.Unsetenv("FAAS_NODE_NAME")
	os.Unsetenv("FAAS_PUBLIC_LISTEN_ADDR")
	os.Unsetenv("FAAS_PUBLIC_CONTROL_ADDR")

	if err := requirePublicBindInMultiHost(); err != nil {
		t.Errorf("single-box posture must accept loopback defaults, got: %v", err)
	}
}

// TestRequirePublicBindInMultiHost_FatalsOnLoopbackDefault pins
// the multi-host safety cluster PR-8 (audit F8-A) check: a
// FAAS_NODE_NAME=node-X env (multi-box posture signal) combined
// with an unset FAAS_PUBLIC_LISTEN_ADDR (loopback default) must
// refuse to boot. The error message must reference the operator
// action they need to take — set FAAS_PUBLIC_LISTEN_ADDR.
func TestRequirePublicBindInMultiHost_FatalsOnLoopbackDefault(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "node-A")
	os.Unsetenv("FAAS_PUBLIC_LISTEN_ADDR")
	t.Setenv("FAAS_PUBLIC_CONTROL_ADDR", "0.0.0.0:9092") // control addr explicitly set

	err := requirePublicBindInMultiHost()
	if err == nil {
		t.Fatal("multi-host posture + unset listen addr must error, got nil")
	}
	if !strings.Contains(err.Error(), "FAAS_PUBLIC_LISTEN_ADDR") {
		t.Errorf("error must mention FAAS_PUBLIC_LISTEN_ADDR, got: %v", err)
	}
	if !strings.Contains(err.Error(), "node-A") {
		t.Errorf("error must mention FAAS_NODE_NAME value, got: %v", err)
	}
}

// TestRequirePublicBindInMultiHost_FatalsOnLoopbackControl pins
// the control listener mirror. A FAAS_NODE_NAME-set box with
// unset FAAS_PUBLIC_CONTROL_ADDR must also refuse.
func TestRequirePublicBindInMultiHost_FatalsOnLoopbackControl(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "node-A")
	t.Setenv("FAAS_PUBLIC_LISTEN_ADDR", "0.0.0.0:443") // listen addr explicitly set
	os.Unsetenv("FAAS_PUBLIC_CONTROL_ADDR")            // control defaulted

	err := requirePublicBindInMultiHost()
	if err == nil {
		t.Fatal("multi-host posture + unset control addr must error, got nil")
	}
	if !strings.Contains(err.Error(), "FAAS_PUBLIC_CONTROL_ADDR") {
		t.Errorf("error must mention FAAS_PUBLIC_CONTROL_ADDR, got: %v", err)
	}
}

// TestRequirePublicBindInMultiHost_AcceptsExplicitOverrideInMultiHost
// pins the escape hatch: an operator who really does want the
// loopback bind (a node behind an external LB) sets the env
// vars explicitly. The check must distinguish "unset → would
// default to loopback" from "explicitly set to loopback".
func TestRequirePublicBindInMultiHost_AcceptsExplicitOverrideInMultiHost(t *testing.T) {
	t.Setenv("FAAS_NODE_NAME", "node-A")
	t.Setenv("FAAS_PUBLIC_LISTEN_ADDR", "127.0.0.1:8443")  // explicit even if loopback
	t.Setenv("FAAS_PUBLIC_CONTROL_ADDR", "127.0.0.1:9092") // explicit even if loopback

	if err := requirePublicBindInMultiHost(); err != nil {
		t.Errorf("explicit loopback override must pass (escape hatch), got: %v", err)
	}
}
