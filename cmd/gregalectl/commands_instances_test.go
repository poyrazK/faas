package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLoadOperatorScheddConfigMissingUsesUnixDefault(t *testing.T) {
	cfg, err := loadOperatorScheddConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("loadOperatorScheddConfig: %v", err)
	}
	if got, want := cfg.Target, "unix:///run/faas/schedd.sock"; got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
	if cfg.CertPath != "" || cfg.KeyPath != "" || cfg.CAPath != "" {
		t.Fatalf("TLS paths = %#v, want empty single-box defaults", cfg)
	}
}

func TestLoadOperatorScheddConfigSplitBox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meterd.toml")
	body := strings.Join([]string{
		`schedd_socket = "tcp://schedd.faas:9091"`,
		`schedd_tls_cert_path = "/etc/faas/tls/meterd/schedd-client.crt"`,
		`schedd_tls_key_path = "/etc/faas/tls/meterd/schedd-client.key"`,
		`schedd_tls_ca_path = "/etc/faas/tls/ca/ca.crt"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadOperatorScheddConfig(path)
	if err != nil {
		t.Fatalf("loadOperatorScheddConfig: %v", err)
	}
	if got, want := cfg.Target, "tcp://schedd.faas:9091"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	if got, want := cfg.CertPath, "/etc/faas/tls/meterd/schedd-client.crt"; got != want {
		t.Errorf("cert path = %q, want %q", got, want)
	}
	if got, want := cfg.KeyPath, "/etc/faas/tls/meterd/schedd-client.key"; got != want {
		t.Errorf("key path = %q, want %q", got, want)
	}
	if got, want := cfg.CAPath, "/etc/faas/tls/ca/ca.crt"; got != want {
		t.Errorf("CA path = %q, want %q", got, want)
	}
}

func TestLoadOperatorScheddConfigRejectsPartialTLS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meterd.toml")
	if err := os.WriteFile(path, []byte(`schedd_tls_cert_path = "/cert.pem"`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := resolveOperatorScheddConnection(path)
	if err == nil || !strings.Contains(err.Error(), "schedd_tls_key_path") || !strings.Contains(err.Error(), "schedd_tls_ca_path") {
		t.Fatalf("partial TLS error = %v, want missing key and CA fields", err)
	}
}

func TestResolveOperatorScheddConnectionEnvTargetWins(t *testing.T) {
	t.Setenv("FAAS_SCHEDD_ADDR", "unix:///tmp/test-schedd.sock")
	target, tlsCfg, err := resolveOperatorScheddConnection(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("resolveOperatorScheddConnection: %v", err)
	}
	if got, want := target, "unix:///tmp/test-schedd.sock"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	if tlsCfg != nil {
		t.Errorf("TLS config = %#v, want nil for missing single-box config", tlsCfg)
	}
}

const testInstanceTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestInstanceRecoveryUsesAuthenticatedDurableIntent(t *testing.T) {
	const intentID = "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name       string
		postPath   string
		accepted   api.OperatorIntentAcceptedResponse
		invoke     func([]string) int
		args       []string
		wantOutput string
	}{
		{
			name:       "force park",
			postPath:   "/v1/admin/instances/22222222-2222-2222-2222-222222222222/force-park",
			accepted:   api.OperatorIntentAcceptedResponse{InstanceID: "22222222-2222-2222-2222-222222222222"},
			invoke:     cmdInstancesForcePark,
			args:       []string{"--instance-id=22222222-2222-2222-2222-222222222222", "--yes"},
			wantOutput: "force-park succeeded",
		},
		{
			name:       "force cold boot",
			postPath:   "/v1/admin/apps/tenant-app/force-cold-boot",
			accepted:   api.OperatorIntentAcceptedResponse{AppID: "app-1", DeploymentID: "deployment-1"},
			invoke:     cmdInstancesForceColdBoot,
			args:       []string{"--app-slug=tenant-app", "--yes"},
			wantOutput: "deployment=deployment-1",
		},
		{
			name:       "force restart",
			postPath:   "/v1/admin/instances/33333333-3333-3333-3333-333333333333/force-restart",
			accepted:   api.OperatorIntentAcceptedResponse{InstanceID: "33333333-3333-3333-3333-333333333333"},
			invoke:     cmdInstancesForceRestart,
			args:       []string{"--instance-id=33333333-3333-3333-3333-333333333333", "--yes"},
			wantOutput: "snap_ids_marked_stale=[snap-warm snap-init]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			postSeen, pollSeen := false, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cookie, err := r.Cookie("faas_sid")
				if err != nil || cookie.Value != "opaque-session" {
					t.Errorf("session cookie = %v, %v", cookie, err)
				}
				if r.UserAgent() != operatorUserAgent {
					t.Errorf("User-Agent = %q", r.UserAgent())
				}
				switch {
				case r.Method == http.MethodPost && r.URL.Path == tc.postPath:
					postSeen = true
					if r.Header.Get("Idempotency-Key") == "" {
						t.Error("missing Idempotency-Key")
					}
					if got := r.Header.Get(operatorTraceIDHeader); got != testInstanceTraceID {
						t.Errorf("trace header = %q, want %q", got, testInstanceTraceID)
					}
					if r.URL.Query().Get("confirm") != "true" || r.URL.Query().Get("reason") != "incident_recovery" {
						t.Errorf("query = %s", r.URL.RawQuery)
					}
					accepted := tc.accepted
					accepted.OK = true
					accepted.IntentID = intentID
					accepted.StatusURL = "/v1/admin/operator-intents/" + intentID
					accepted.Reason = "incident_recovery"
					writeTestJSON(w, http.StatusAccepted, accepted)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/operator-intents/"+intentID:
					pollSeen = true
					writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{
						IntentID: intentID, Status: "succeeded", TraceID: testInstanceTraceID,
						SnapIDsMarkedStale: []string{"snap-warm", "snap-init"},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			installTestOperatorSession(t, server.URL, "opaque-session")
			out, stderr, restore := captureOperatorIO()
			defer restore()
			args := append([]string(nil), tc.args...)
			args = append(args, "--reason=incident_recovery", "--trace-id="+testInstanceTraceID, "--timeout=1s")
			if code := tc.invoke(args); code != 0 {
				t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
			}
			if !postSeen || !pollSeen {
				t.Fatalf("postSeen=%t pollSeen=%t", postSeen, pollSeen)
			}
			if !strings.Contains(out.String(), "intent="+intentID) || !strings.Contains(out.String(), tc.wantOutput) {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

func TestInstanceRecoveryExplainsStepUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusForbidden, api.Problem{
			Status: http.StatusForbidden,
			Code:   api.CodeStepUpRequired,
			Detail: "fresh MFA proof required",
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	_, stderr, restore := captureOperatorIO()
	defer restore()

	code := cmdInstancesForcePark([]string{
		"--instance-id=22222222-2222-2222-2222-222222222222",
		"--yes",
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gregalectl auth step-up") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInstanceBreakGlassRequiresExplicitReason(t *testing.T) {
	tests := []struct {
		name   string
		invoke func([]string) int
		args   []string
	}{
		{"force park", cmdInstancesForcePark, []string{"--instance-id=22222222-2222-2222-2222-222222222222"}},
		{"force cold boot", cmdInstancesForceColdBoot, []string{"--app-slug=tenant-app"}},
		{"force restart", cmdInstancesForceRestart, []string{"--instance-id=33333333-3333-3333-3333-333333333333"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, restore := captureOperatorIO()
			defer restore()
			args := append([]string(nil), tc.args...)
			args = append(args, "--yes", "--break-glass-local")
			if code := tc.invoke(args); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "explicit --reason") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestInstanceIntentFailureReturnsNonZero(t *testing.T) {
	const intentID = "11111111-1111-1111-1111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeTestJSON(w, http.StatusAccepted, api.OperatorIntentAcceptedResponse{
				OK: true, IntentID: intentID, StatusURL: "/v1/admin/operator-intents/" + intentID,
			})
			return
		}
		writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{
			IntentID: intentID, Status: "failed", Error: "instance changed state",
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	_, stderr, restore := captureOperatorIO()
	defer restore()

	code := cmdInstancesForceRestart([]string{
		"--instance-id=33333333-3333-3333-3333-333333333333",
		"--yes", "--timeout=" + time.Second.String(),
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "instance changed state") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
