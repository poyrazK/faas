// adr: 1037 — interactive admission is separately gated and persists only the
// attach token's digest.

package apptaskattach

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnabledFromEnv(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"", false}, {"0", false}, {"true", false}, {"1", true}, {" 1 ", true}} {
		got := EnabledFromEnv(func(key string) string {
			if key == "FAAS_INTERACTIVE_APP_TASKS" {
				return tc.value
			}
			return ""
		})
		if got != tc.want {
			t.Fatalf("FAAS_INTERACTIVE_APP_TASKS=%q enabled = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestMint(t *testing.T) {
	if _, problem := Mint(false); problem == nil || problem.Status != http.StatusNotImplemented {
		t.Fatalf("Mint with gate off = %+v, want 501", problem)
	}
	first, problem := Mint(true)
	if problem != nil || len(first) != 43 {
		t.Fatalf("Mint = %q %+v", first, problem)
	}
	if second, _ := Mint(true); second == first {
		t.Fatal("Mint returned the same token twice")
	}
}

func TestSession(t *testing.T) {
	for name, tc := range map[string]struct {
		interactive bool
		token       string
		wantErr     bool
	}{
		"ordinary":                  {false, "", false},
		"interactive":               {true, "tok", false},
		"interactive no token":      {true, "", true},
		"token without interactive": {false, "tok", true},
	} {
		session, err := Session(tc.interactive, true, tc.token)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: err = %v", name, err)
		}
		if tc.wantErr || !tc.interactive {
			if session != nil {
				t.Fatalf("%s: session = %+v, want nil", name, session)
			}
			continue
		}
		digest := sha256.Sum256([]byte(tc.token))
		if !session.TTY || !bytes.Equal(session.TokenSHA256, digest[:]) {
			t.Fatalf("%s: session = %+v", name, session)
		}
	}
}

func TestRespond(t *testing.T) {
	rec := httptest.NewRecorder()
	resp := api.AppTaskResponse{ID: "task-1"}
	Respond(rec, &resp, "shell", "")
	if resp.Attach != nil || rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("ordinary task response changed: %+v", resp)
	}
	Respond(rec, &resp, "shell", "tok")
	if !resp.Interactive || resp.Attach == nil || resp.Attach.Token != "tok" ||
		resp.Attach.Path != api.AppTaskAttachPath("shell", "task-1") || resp.Attach.Subprotocol != api.AppTaskAttachSubprotocol {
		t.Fatalf("interactive response = %+v", resp)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}
