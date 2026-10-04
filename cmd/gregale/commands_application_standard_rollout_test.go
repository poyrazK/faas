package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOrgStandardRolloutCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "fixture")
	id := uuid.NewString()
	for _, action := range []string{"approve", "pause", "resume", "abort"} {
		t.Run(action, func(t *testing.T) {
			kind, body := "operation", `{"expected_updated_at":"2026-10-04T12:00:00.123456Z"}`
			path := "/v1/orgs/acme/application-standard-operations/" + id + "/" + action
			if action == "approve" {
				kind, body, path = "reviews", `{"approval_hash":"`+strings.Repeat("a", 64)+`"}`, "/v1/orgs/acme/application-standard-reviews/"+id+"/approve"
			}
			file := filepath.Join(t.TempDir(), "rollout.json")
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			called := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.Method != http.MethodPost || r.URL.RequestURI() != path || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Errorf("rollout path: %s %s", r.Method, r.URL.RequestURI())
				}
				var got, want any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(body), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("lost exact approval/control token: %+v", got)
				}
				writeJSONTestStatus(w, http.StatusOK, api.ApplicationStandardOperation{ID: id, State: "waiting"})
			}))
			defer ts.Close()
			t.Setenv("FAAS_API", ts.URL)
			stdout, restore := captureStdout(t)
			defer restore()
			if code := cmdOrgs([]string{"standards", kind, action, "--org", "acme", "--id", id, "--file", file}); code != 0 || called != 1 || !json.Valid([]byte(stdout.String())) {
				t.Fatalf("CLI exit=%d calls=%d output=%s", code, called, stdout.String())
			}
		})
	}
}

func TestOrgStandardRolloutCLIRefusalsBeforeNetwork(t *testing.T) {
	id := uuid.NewString()
	for _, args := range [][]string{nil, {"--org", "acme", "--id", id}, {"--org", "acme", "--id", "bad", "--file", "body.json"}, {"--org", "acme", "--id", uuid.Nil.String(), "--file", "body.json"}, {"--org", "acme", "--id", id, "--file", "body.json", "extra"}} {
		if _, err := parseStandardRolloutCLI("pause", args); err == nil {
			t.Fatalf("accepted invalid rollout arguments %v", args)
		}
	}
	if _, err := parseStandardRolloutCLI("unknown", nil); err == nil {
		t.Fatal("unknown action accepted")
	}
	t.Setenv("FAAS_TOKEN", "fixture")
	called := 0
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	defer ts.Close()
	t.Setenv("FAAS_API", ts.URL)
	for _, body := range []string{`{}`, `{"expected_updated_at":"2026-10-04T12:00:00.123456789Z"}`, `{"expected_updated_at":"2026-10-04T12:00:00Z","private":true}`, strings.Repeat("x", api.ApplicationStandardMaxDefinitionBytes+1)} {
		file := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if code := cmdOrgs([]string{"standards", "operation", "pause", "--org", "acme", "--id", id, "--file", file}); code == 0 || called != 0 {
			t.Fatalf("invalid body reached network: exit=%d calls=%d", code, called)
		}
	}
}
