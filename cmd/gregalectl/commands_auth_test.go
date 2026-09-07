package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperatorSessionRoundTripRequiresPrivateFile(t *testing.T) {
	path := t.TempDir() + "/operator-session.json"
	t.Setenv(operatorSessionFile, path)
	t.Setenv("FAAS_APID_URL", "https://api.gregale.test")
	want := operatorSession{
		BaseURL:   "https://api.gregale.test",
		Email:     "operator@gregale.test",
		AccountID: "account-1",
		Cookie:    "opaque-session",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := saveOperatorSession(want); err != nil {
		t.Fatalf("saveOperatorSession: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session mode = %04o, want 0600", got)
	}
	got, err := loadOperatorSession()
	if err != nil {
		t.Fatalf("loadOperatorSession: %v", err)
	}
	if got.Cookie != want.Cookie || got.AccountID != want.AccountID {
		t.Fatalf("loaded session = %+v", got)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOperatorSession(); err == nil || !strings.Contains(err.Error(), "want 0600") {
		t.Fatalf("world-readable session load error = %v", err)
	}
}

func TestMutateComputeNodeViaAPIUsesSessionAndPollsIntent(t *testing.T) {
	const intentID = "11111111-1111-1111-1111-111111111111"
	var postSeen, pollSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("faas_sid")
		if err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		if r.UserAgent() != operatorUserAgent {
			t.Errorf("User-Agent = %q", r.UserAgent())
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/ops/nodes/node-a/drain":
			postSeen = true
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing Idempotency-Key")
			}
			if r.URL.Query().Get("confirm") != "true" || r.URL.Query().Get("reason") != "image_upgrade" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusAccepted, api.ObsNodeMutationResponse{
				OK: true, IntentID: intentID, StatusURL: "/v1/admin/operator-intents/" + intentID,
				RequestedLifecycle: "maintenance",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/operator-intents/"+intentID:
			pollSeen = true
			writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{IntentID: intentID, Status: "succeeded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := mutateComputeNodeViaAPI("node-a", "drain", "image_upgrade", time.Second); code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !postSeen || !pollSeen {
		t.Fatalf("postSeen=%t pollSeen=%t", postSeen, pollSeen)
	}
	if !strings.Contains(out.String(), "status=succeeded") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestComputeNodeDrainStatusRequiresMaintenance(t *testing.T) {
	lifecycle := "draining"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, http.StatusOK, computeNodeDrainResponse{NodeName: "node-a", Lifecycle: lifecycle})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()

	if code := computeNodeDrainStatusViaAPI("node-a"); code != 1 {
		t.Fatalf("draining exit = %d, stderr=%s", code, stderr.String())
	}
	lifecycle = "maintenance"
	out.Reset()
	if code := computeNodeDrainStatusViaAPI("node-a"); code != 0 {
		t.Fatalf("maintenance exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "drain-safe") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestOperatorHTTPErrorExplainsStepUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusForbidden, api.Problem{Status: http.StatusForbidden, Code: api.CodeStepUpRequired})
	}))
	defer server.Close()
	sess := operatorSession{BaseURL: server.URL, Cookie: "opaque-session"}
	err := newOperatorHTTPClient(&sess).doJSON(t.Context(), http.MethodPost, "/mutation", nil, nil, true, nil)
	if err == nil || !strings.Contains(err.Error(), "gregalectl auth step-up") {
		t.Fatalf("error = %v", err)
	}
}

func installTestOperatorSession(t *testing.T, baseURL, cookie string) {
	t.Helper()
	path := t.TempDir() + "/operator-session.json"
	t.Setenv(operatorSessionFile, path)
	t.Setenv("FAAS_APID_URL", baseURL)
	t.Setenv(operatorSessionEnv, "")
	if err := saveOperatorSession(operatorSession{BaseURL: baseURL, Cookie: cookie, ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func captureOperatorIO() (*bytes.Buffer, *bytes.Buffer, func()) {
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	oldOut, oldErr := osStdout, osStderr
	oldJSON := jsonOutput
	osStdout, osStderr = out, stderr
	jsonOutput = false
	return out, stderr, func() {
		osStdout, osStderr = oldOut, oldErr
		jsonOutput = oldJSON
	}
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
