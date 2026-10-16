package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type campaignStatusLiveReaderFake struct {
	faults *campaignFaultHistoryAPI
	mode   string
	reads  int
}

func (c *campaignStatusLiveReaderFake) check(ctx context.Context) error {
	c.reads++
	if c.mode == "outage" {
		return recoveryUnavailable()
	}
	if c.mode == "deadline" {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (c *campaignStatusLiveReaderFake) GetAPIConsumer(ctx context.Context, slug, id string) (api.APIConsumerResponse, error) {
	if err := c.check(ctx); err != nil {
		return api.APIConsumerResponse{}, err
	}
	return c.faults.GetAPIConsumer(ctx, slug, id)
}
func (c *campaignStatusLiveReaderFake) GetScenarioTestRegistration(ctx context.Context, run string) (api.ScenarioTestRegistration, error) {
	if err := c.check(ctx); err != nil {
		return api.ScenarioTestRegistration{}, err
	}
	if c.mode == "registration404" {
		return api.ScenarioTestRegistration{}, faultHistoryNotFound()
	}
	return c.faults.GetScenarioTestRegistration(ctx, run)
}
func (c *campaignStatusLiveReaderFake) GetDevSession(ctx context.Context, project, run string) (api.DevSessionResponse, error) {
	if err := c.check(ctx); err != nil {
		return api.DevSessionResponse{}, err
	}
	return c.faults.GetDevSession(ctx, project, run)
}
func (c *campaignStatusLiveReaderFake) LookupScenarioTestConsumer(context.Context, string, string, api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error) {
	return api.ScenarioTestConsumerReceipt{}, faultHistoryNotFound()
}

func TestCampaignStatusLiveResourceProofs(t *testing.T) {
	// adr: 984 — read-only status verifies each immutable identity and never reuses cached absence during outages.
	for _, mode := range []string{"present", "clean", "outage", "registration404", "intent404", "consumer_changed", "consumer_unknown_status", "members_changed", "session_changed", "cancelled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			f := newCampaignFaultHistoryFixture(t)
			sources, err := collectTestCampaignFaultHistory(f.plan, f.cp, f.scenarios, f.dirs[2], nil)
			if err != nil {
				t.Fatal(err)
			}
			backend := newCampaignFaultHistoryAPI(t, f)
			client := &campaignStatusLiveReaderFake{faults: backend, mode: mode}
			previous := &testCampaignStatusWatchProgress{}
			for _, source := range sources {
				for _, j := range source.Journals {
					previous.Faults = append(previous.Faults, testCampaignFaultObservation{RunID: j.RunID, Registration: "absent", Consumers: map[string]string{"customer": "revoked"}})
				}
				for _, target := range source.Targets {
					previous.Sessions = append(previous.Sessions, testCampaignCleanupAction{RunID: target.RunID, Project: target.Project, Status: "deleted"})
				}
			}
			first := sources[0].Journals[0]
			consumer := first.Faults.Consumers["customer"]
			switch mode {
			case "clean":
				for id, c := range backend.consumers {
					c.Status = "revoked"
					backend.consumers[id] = c
				}
				for run := range backend.registrations {
					backend.registrations[run] = map[string]string{}
				}
				backend.sessions = map[string]string{}
			case "intent404":
				consumer.ID = ""
				consumer.CreationIntentSHA256, _ = api.ScenarioTestConsumerIntent(first.RunID, "customer", api.ScenarioTestConsumerRequest{AppID: consumer.AppID, ExpiresAt: consumer.ExpiresAt})
				sources[0].Journals[0].Faults.Consumers["customer"] = consumer
			case "consumer_changed":
				c := backend.consumers[consumer.ID]
				c.ExternalRef = "foreign"
				backend.consumers[consumer.ID] = c
			case "consumer_unknown_status":
				c := backend.consumers[consumer.ID]
				c.Status = "unknown"
				backend.consumers[consumer.ID] = c
			case "members_changed":
				backend.registrations[first.RunID] = map[string]string{"checkout": "foreign"}
			case "session_changed":
				backend.sessions["checkout/"+first.RunID] = "foreign"
			}
			ctx := context.WithValue(context.Background(), testCampaignRecoveryPolicyKey{}, testCampaignRecoveryPolicy{Timeout: 10 * time.Millisecond, Backoff: time.Millisecond, MaxBackoff: time.Millisecond, Attempts: 3})
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 15*time.Millisecond)
				defer cancel()
			}
			frozen := campaignFaultFrozenBytes(t, []string{f.root})
			result := verifyTestCampaignStatusLive(ctx, client, sources, previous)
			if len(result.Resources) != 9 {
				t.Fatal("live check lost inventory", len(result.Resources))
			}
			if mode == "clean" {
				if !result.AllVerified || !result.ResourcesClean || result.State != "verified_clean" {
					t.Fatal("clean resources were not verified")
				}
			} else if result.ResourcesClean {
				t.Fatal("unverified or present resources became clean")
			}
			if mode == "present" && !result.AllVerified {
				t.Fatal("present resources did not verify")
			}
			for _, resource := range result.Resources {
				if resource.State == "unverified" {
					if resource.VerifiedAt != nil || resource.Drift != "unverified" || resource.Issue == nil {
						t.Fatal("unknown resource retained proof")
					}
				} else if resource.VerifiedAt == nil || resource.CheckedAt == nil {
					t.Fatal("verified resource lacks timestamp")
				}
				if mode == "clean" && resource.Drift != "unchanged" {
					t.Fatal("confirmed session deletion became false drift", resource)
				}
				if mode == "present" && resource.Drift != "changed" {
					t.Fatal("resource changes were not reported")
				}
			}
			if mode != "present" && mode != "clean" && result.AllVerified {
				t.Fatal("invalid proof passed verification", mode)
			}
			if mode == "outage" && client.reads != 27 {
				t.Fatal("retry budget was not enforced", client.reads)
			}
			if mode == "cancelled" && client.reads != 0 {
				t.Fatal("cancelled verification made requests")
			}
			if mode == "deadline" && client.reads > 2 {
				t.Fatal("operation deadline did not stop more reads")
			}
			if backend.revokes != 0 || backend.deletes != 0 || backend.namespaces != 0 {
				t.Fatal("live status mutated resources")
			}
			report := testCampaignStatusReport{SnapshotStable: true, LiveVerification: result}
			invalidateTestCampaignStatusLive(&report)
			if report.SnapshotStable || result.AllVerified || result.ResourcesClean {
				t.Fatal("changed local snapshot retained live proof")
			}
			for _, r := range result.Resources {
				if r.VerifiedAt != nil || r.State != "unverified" {
					t.Fatal("invalidated live state remained verified")
				}
			}
			assertCampaignFaultFrozen(t, frozen, []string{f.root})
		})
	}
}

func TestCampaignStatusLiveCancellationProof(t *testing.T) {
	// adr: 984 — consumer cancellation and pending intent observations require versioned receipts; missing routes are not absence.
	ctx := context.Background()
	req := api.ScenarioTestConsumerRequest{AppID: "app", ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
	digest, err := api.ScenarioTestConsumerIntent("11111111111111111111111111111111", "customer", req)
	if err != nil {
		t.Fatal(err)
	}
	c := testCampaignFaultConsumer{AppID: req.AppID, ExpiresAt: req.ExpiresAt, CreationIntentSHA256: digest}
	for _, cancelled := range []bool{false, true} {
		reader := &campaignStatusLiveIntentReader{receipt: api.ScenarioTestConsumerReceipt{SchemaVersion: 2, IntentSHA256: digest, ExpiresAt: req.ExpiresAt, Cancelled: cancelled}}
		target := testCampaignStatusLiveTarget{Resource: testCampaignStatusLiveResource{Kind: "consumer", RunID: "11111111111111111111111111111111", Name: "customer"}, Consumer: c}
		state, err := readTestCampaignStatusLiveResource(ctx, reader, target)
		expected := "pending"
		if cancelled {
			expected = "cancelled"
		}
		if err != nil || state != expected {
			t.Fatal("intent proof changed meaning", state, err)
		}
	}
}

type campaignStatusLiveIntentReader struct {
	campaignStatusLiveReaderFake
	receipt api.ScenarioTestConsumerReceipt
}

func (c *campaignStatusLiveIntentReader) LookupScenarioTestConsumer(context.Context, string, string, api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error) {
	return c.receipt, nil
}

func TestCampaignStatusLiveCLI(t *testing.T) {
	// adr: 984 — only explicit live status performs GETs; exports preserve evidence and never certify execution.
	for _, mode := range []string{"offline", "live", "snapshot", "profile"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "profile" && runtime.GOOS != "linux" {
				t.Skip("watch receipts require Linux process identity")
			}
			f := newCampaignFaultHistoryFixture(t)
			paths := testCampaignWatchPaths{Plan: filepath.Join(f.root, "plan.json"), Policy: filepath.Join(f.root, "policy.yaml"), Manifest: filepath.Join(f.root, "manifest.yaml"), Directory: f.dirs[2]}
			mustCampaignFaultJSON(t, paths.Plan, f.plan)
			if mode == "profile" {
				w, fixture := newCampaignWatchReceiptFixture(t)
				paths, f = w.Paths, fixture
			}
			backend := newCampaignFaultHistoryAPI(t, f)
			var requests, mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" {
					mutations.Add(1)
					http.Error(w, "mutation forbidden", 405)
					return
				}
				if mode == "snapshot" && requests.Load() == 1 {
					entries, err := readTestCampaignFaultJournals(paths.Directory, "a", f.scenarios["checkout"])
					if err != nil {
						t.Error(err)
						http.Error(w, "fixture unavailable", 500)
						return
					}
					entries[0].Faults.Registration = "changed"
					if err := writeTestCampaignJSON(filepath.Join(testCampaignJournalDirectory(paths.Directory, "a"), entries[0].RunID+".json"), entries[0]); err != nil {
						t.Error(err)
					}
				}
				fields := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
				var value any
				var err error
				switch {
				case len(fields) == 4 && fields[2] == "test-runs":
					value, err = backend.GetScenarioTestRegistration(r.Context(), fields[3])
				case len(fields) == 5 && fields[3] == "consumers":
					value, err = backend.GetAPIConsumer(r.Context(), fields[2], fields[4])
				case len(fields) == 4 && fields[2] == "sessions":
					value, err = backend.GetDevSession(r.Context(), fields[3], r.URL.Query().Get("workspace_id"))
				default:
					http.NotFound(w, r)
					return
				}
				if err != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "live-status-test")
			before := campaignFaultFrozenBytes(t, []string{f.root})
			output, html := filepath.Join(t.TempDir(), "status.json"), filepath.Join(t.TempDir(), "status.html")
			args := []string{"--plan", paths.Plan, "--policy", paths.Policy, "--manifest", paths.Manifest, "--directory", paths.Directory, "--json", output, "--html", html}
			if mode != "offline" {
				args = append(args, "--verify-live")
			}
			if code := cmdTestCampaignStatus(args); code != 0 {
				t.Fatal(code)
			}
			var report testCampaignStatusReport
			if err := readTestCampaignJSON(output, &report); err != nil {
				t.Fatal(err)
			}
			if mode == "offline" {
				if requests.Load() != 0 || report.LiveVerification != nil {
					t.Fatal("default status accessed the API")
				}
			} else {
				live := report.LiveVerification
				if live == nil || len(live.Resources) != 9 {
					t.Fatal("missing live inventory")
				}
				switch mode {
				case "live":
					if requests.Load() != 9 || !live.AllVerified || live.ResourcesClean || live.State != "verified_resources_present" {
						t.Fatal("live resource check failed", requests.Load(), live.State)
					}
				case "snapshot":
					if live.AllVerified || live.ResourcesClean || live.SnapshotStable || report.SnapshotStable || live.State != "snapshot_changed" {
						t.Fatal("changed evidence retained live proof")
					}
				case "profile":
					if requests.Load() != 0 || live.State != "blocked" || live.ResourcesClean || live.AllVerified {
						t.Fatal("changed profile queried a different API")
					}
				}
			}
			if mutations.Load() != 0 || backend.revokes != 0 || backend.deletes != 0 || backend.namespaces != 0 {
				t.Fatal("status mutated resources")
			}
			if report.Counts["completed"] != 0 || !report.Informational || !report.ReadOnly {
				t.Fatal("status changed execution semantics")
			}
			body, err := os.ReadFile(html)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "offline" && !bytes.Contains(body, []byte("Live resource verification")) {
				t.Fatal("HTML omitted live state")
			}
			if mode != "snapshot" {
				assertCampaignFaultFrozen(t, before, []string{f.root})
			}
		})
	}
}
