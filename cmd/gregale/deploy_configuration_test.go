package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type configurationFixture struct {
	response      api.DeploymentResponse
	reads, writes int
}

func (f *configurationFixture) GetDeployment(context.Context, string) (api.DeploymentResponse, error) {
	f.reads++
	return f.response, nil
}
func (f *configurationFixture) GetDeploymentCronStagingSnapshot(context.Context, string) (api.DeploymentCronStagingSnapshot, error) {
	f.writes++
	return api.DeploymentCronStagingSnapshot{}, errors.New("unexpected staging snapshot")
}
func (f *configurationFixture) ApplyDeploymentCronStaging(context.Context, string, api.DeploymentCronStagingRequest) (api.DeploymentStagingReceipt, error) {
	f.writes++
	return api.DeploymentStagingReceipt{}, errors.New("unexpected configuration mutation")
}

func TestSavedDeployConfigurationReadOnly(t *testing.T) {
	for _, mode := range []string{"pending", "activated", "conflicted", "discarded", "newer-intent", "wrong-app"} {
		t.Run(mode, func(t *testing.T) {
			id := strings.Repeat("a", 32)
			s := &savedDeploySession{path: filepath.Join(t.TempDir(), id+".json"), record: deploySessionRecord{ID: id, AppID: "app", DeploymentID: "dep", Outcome: deploySubmissionAccepted, CronJournal: &deployCronJournal{Managed: true, Deferred: true, Status: "prepared"}}}
			internal, reason := "prepared", "awaiting_full_traffic"
			switch mode {
			case "activated":
				internal, reason = "activated", ""
			case "conflicted":
				internal, reason = "conflict", "cron_changed"
			case "discarded":
				internal, reason = "discarded", "deployment_failed"
			}
			configuration := api.NewDeploymentConfigurationStatus(internal, reason)
			configuration.Crons.SessionID = id
			f := &configurationFixture{response: api.DeploymentResponse{ID: "dep", AppID: "app", Status: statusLive, Configuration: &configuration}}
			if mode == "newer-intent" {
				configuration.Crons.SessionID = strings.Repeat("b", 32)
			}
			if mode == "wrong-app" {
				f.response.AppID = "other-app"
			}
			err := s.refreshCronActivation(t.Context(), f)
			wantErr := mode == "conflicted" || mode == "discarded" || mode == "newer-intent" || mode == "wrong-app"
			if (err != nil) != wantErr || f.reads != 1 || f.writes != 0 {
				t.Fatalf("err=%v reads=%d writes=%d", err, f.reads, f.writes)
			}
			if mode == "wrong-app" {
				if s.record.CronConfiguration != nil {
					t.Fatal("retained another app's diagnostic")
				}
				return
			}
			if mode == "activated" {
				configuration.Crons.SessionID = strings.Repeat("b", 32)
				if err := s.refreshCronActivation(t.Context(), f); err != nil || f.reads != 1 {
					t.Fatalf("completed activation was reclassified: %v reads=%d", err, f.reads)
				}
			}
			if mode == "conflicted" {
				newer := api.NewDeploymentConfigurationStatus("prepared", "awaiting_readiness")
				newer.Crons.SessionID = strings.Repeat("b", 32)
				f.response.Configuration = &newer
				if err := s.refreshCronActivation(t.Context(), f); err == nil || s.record.CronConfiguration.Crons.Reason != "cron_changed" || f.writes != 0 {
					t.Fatalf("original conflict was lost: %v configuration=%+v", err, s.record.CronConfiguration)
				}
			}
			if mode == "newer-intent" && s.record.CronConfiguration.Crons.Reason != "intent_superseded" {
				t.Fatalf("configuration=%+v", s.record.CronConfiguration)
			}
			receipt := newDeployReceipt(f.response, nil, "", "")
			s.decorate(receipt)
			if receipt.Configuration == nil || receipt.ConfigurationComplete == nil || *receipt.ConfigurationComplete != (mode == "activated") {
				t.Fatalf("receipt=%+v", receipt)
			}
			data, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			var saved deploySessionRecord
			if err := json.Unmarshal(data, &saved); err != nil || saved.CronConfiguration == nil {
				t.Fatalf("saved=%+v err=%v", saved, err)
			}
			if wantErr && s.recovery("configuration").Configuration == nil {
				t.Fatal("JSON recovery omitted diagnostic")
			}
		})
	}
}

func TestCmdDeploymentGetConfigurationReadOnly(t *testing.T) {
	const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	configuration := api.NewDeploymentConfigurationStatus("conflict", "cron_changed")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/deployments/"+id {
			t.Errorf("unexpected request=%s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: "app", Status: statusLive, Configuration: &configuration})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-configuration-token")
	var output bytes.Buffer
	prior, jsonPrior := osStdout, jsonOutput
	osStdout = &output
	jsonOutput = false
	t.Cleanup(func() { osStdout = prior; jsonOutput = jsonPrior })
	if code := cmdDeploymentGet([]string{id}); code != 0 {
		t.Fatalf("exit=%d output=%s", code, &output)
	}
	if calls != 1 || !strings.Contains(output.String(), "release live, configuration incomplete") || !strings.Contains(output.String(), "Cron configuration changed") {
		t.Fatalf("calls=%d output=%s", calls, &output)
	}
}

func TestCmdDeploysStatusConfigurationReadOnly(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
			const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			configuration := api.NewDeploymentConfigurationStatus("conflict", "cron_changed")
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet {
					t.Errorf("status mutated server: %s", r.Method)
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/v1/deployments/" + id + "/stages":
					_, _ = w.Write([]byte(`{"current":"readiness","history":[]}`))
				case "/v1/deployments/" + id:
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: "app", Status: statusLive, Configuration: &configuration})
				default:
					t.Errorf("unexpected request=%s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-configuration-token")
			var output bytes.Buffer
			prior, jsonPrior := osStdout, jsonOutput
			osStdout = &output
			jsonOutput = asJSON
			t.Cleanup(func() { osStdout = prior; jsonOutput = jsonPrior })
			if code := cmdDeploys([]string{"status", id}); code != 0 {
				t.Fatalf("exit=%d output=%s", code, &output)
			}
			if calls != 2 {
				t.Fatalf("status made %d requests", calls)
			}
			if asJSON {
				var out api.DeploymentResponse
				if err := json.Unmarshal(output.Bytes(), &out); err != nil || out.Configuration == nil || out.Configuration.Crons.Reason != "cron_changed" {
					t.Fatalf("JSON=%s err=%v", &output, err)
				}
			} else if !strings.Contains(output.String(), "release live, configuration incomplete") {
				t.Fatalf("output=%s", &output)
			}
		})
	}
}

func TestServerManagedCronsRenderWithoutLocalJournal(t *testing.T) {
	configuration := api.NewDeploymentConfigurationStatus("prepared", "awaiting_full_traffic")
	var output bytes.Buffer
	prior := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = prior })
	renderCronActivation(t.Context(), api.DeploymentResponse{Status: statusLive, Configuration: &configuration})
	if !strings.Contains(output.String(), "release live, configuration incomplete") || !strings.Contains(output.String(), "100% of production traffic") {
		t.Fatalf("output=%s", &output)
	}
}
