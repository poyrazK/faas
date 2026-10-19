package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOrgStandardApplicationCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	appID := uuid.NewString()
	called := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/orgs/acme/application-standard-enrollments/"+appID {
			t.Errorf("enrollment read: %s %s", r.Method, r.URL.Path)
		}
		writeJSONTestStatus(w, http.StatusOK, api.ApplicationStandardEnrollment{AppID: appID, DesiredRevision: 2, PersistedRevision: 1, ObservedRevision: 0, State: "pending"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdOrgs([]string{"standards", "application", "--org", "acme", "--app", appID}); code != 0 {
		t.Fatalf("enrollment CLI exit: %d", code)
	}
	var got api.ApplicationStandardEnrollment
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatal(err)
	}
	if called != 1 || got.AppID != appID || got.DesiredRevision != 2 || got.PersistedRevision != 1 || got.ObservedRevision != 0 {
		t.Fatalf("CLI lost enrollment progress: %+v requests=%d", got, called)
	}
}

func TestOrgStandardApplicationCLIValidation(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--org", "acme"}, {"--org", "acme", "--app", "bad"},
		{"--org", "acme", "--app", uuid.Nil.String()}, {"--app", uuid.NewString()},
		{"--org", "acme", "--app", uuid.NewString(), "extra"},
	} {
		if _, _, err := parseStandardApplicationCLI(args); err == nil {
			t.Fatalf("accepted invalid application view arguments: %v", args)
		}
	}
}
