package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261015161000000: capture bounded, sanitized sections with honest
// coverage, fixed incident identity, a shared deadline, and create-only files.
const obsBundleTestRef = "deployment/11111111-1111-4111-8111-111111111111"

func obsBundleTestSnapshot(ref string, limit, historyLimit int) obsIncidentBundle {
	now := time.Date(2026, 10, 15, 16, 0, 0, 0, time.UTC)
	kind, id, _ := strings.Cut(ref, "/")
	b := obsIncidentBundle{
		Evidence: obsBundleSection[api.ObsIncidentEvidenceResponse]{Data: api.ObsIncidentEvidenceResponse{
			GeneratedAt: now, Since: now.Add(-24 * time.Hour), Limit: historyLimit,
			Incident: api.ObsIncident{ID: ref, Type: kind, ResourceID: id, Status: "failed", ObservedAt: now.Add(-time.Hour)},
			History:  []api.ObsIncidentEvidenceEntry{{ID: "9007199254740993", Source: "event", Kind: "operator.action.force_restart", At: now}}, UnavailableSources: []string{},
		}},
		Context: obsBundleSection[api.ObsIncidentContextResponse]{Data: api.ObsIncidentContextResponse{
			IncidentID: ref, IncidentStatus: "failed", IncidentObservedAt: now.Add(-time.Hour), AsOf: now.Add(time.Second),
			Scope: api.ObsIncidentContextScope{State: "known", Basis: "app"}, Counts: &api.ObsIncidentContextCounts{Apps: 1, Tenants: 1},
			Limit: limit, TimelineLimit: historyLimit, Since: now.Add(-24 * time.Hour), Until: now,
			Resources: []api.ObsIncidentRelatedResource{}, Timeline: []api.ObsIncidentRelatedEntry{}, TimelineScope: "incident_scope_and_returned_resources", UnavailableSources: []string{},
		}},
		Health: obsBundleSection[api.ObsHealthResponse]{Data: api.ObsHealthResponse{
			GeneratedAt: now.Add(2 * time.Second), PrometheusAvailable: true,
			Signals: map[string]api.ObsHealthSignal{}, AlertHistory: &api.ObsAlertCollectionHealth{State: "current", CollectionState: "current", LastSuccessAt: &now},
			AlertRetention: &api.ObsAlertRetentionHealth{State: "current", AsOf: now, Cutoff: now.Add(-api.ObsAlertHistoryRetention), LastSuccessAt: &now},
		}},
	}
	for _, name := range []string{"audit_log_write_total_5m", "audit_log_write_failures_5m", "alerts_firing"} {
		b.Health.Data.Signals[name] = api.ObsHealthSignal{State: api.ObsHealthSignalHealthy, ObservedAt: &now}
	}
	return b
}

func obsBundleTestStreams(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &out, &stderr, false
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	return &out, &stderr
}

func obsBundleTestSection(w http.ResponseWriter, r *http.Request, b obsIncidentBundle) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/evidence"):
		_ = json.NewEncoder(w).Encode(b.Evidence.Data)
	case strings.HasSuffix(r.URL.Path, "/context"):
		_ = json.NewEncoder(w).Encode(b.Context.Data)
	default:
		_ = json.NewEncoder(w).Encode(b.Health.Data)
	}
}

func TestCmdObsBundleFileCaptureAndJSONReceipt(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	snapshot := obsBundleTestSnapshot(obsBundleTestRef, 2, 3)
	snapshot.Evidence.Data.Truncated = true
	snapshot.Evidence.Data.UnavailableSources = []string{"prometheus_alert_metadata"}
	snapshot.Context.Data.NextCursor = "opaque-next-page"
	snapshot.Context.Data.TimelineTruncated = true
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer explicit-token" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("incorrect method or authentication")
		}
		if strings.HasSuffix(r.URL.Path, "/context") && (r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("history_limit") != "3" || r.URL.Query().Get("cursor") != "") {
			t.Errorf("incorrect context bounds: %s", r.URL)
		}
		if strings.HasSuffix(r.URL.Path, "/evidence") && r.URL.Query().Get("limit") != "3" {
			t.Errorf("incorrect evidence bound")
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		// Unknown sensitive payload fields must disappear during typed decoding.
		recorder := httptest.NewRecorder()
		obsBundleTestSection(recorder, r, snapshot)
		body := bytes.TrimSpace(recorder.Body.Bytes())
		_, _ = w.Write(append(append([]byte{}, body[:len(body)-1]...), []byte(",\"unexpected_raw_payload\":\"private-secret\"}")...))
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "environment-token")
	path := filepath.Join(t.TempDir(), "incident.json")
	jsonOutput = true
	if code := cmdObsDispatch([]string{subObsBundle, "--incident", obsBundleTestRef, "--output", path, "--limit", "2", "--history-limit", "3", "--admin-token", "explicit-token"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 3 || paths[0] != "/v1/admin/obs/incidents/"+obsBundleTestRef+"/evidence" || paths[1] != "/v1/admin/obs/incidents/"+obsBundleTestRef+"/context" || paths[2] != "/v1/admin/obs/health" {
		t.Fatalf("unexpected reads: %v", paths)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var b obsIncidentBundle
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion != obsIncidentBundleSchema || b.IncidentID != obsBundleTestRef || b.RequestedIncidentID != obsBundleTestRef || b.Evidence.Data.History[0].ID != "9007199254740993" {
		t.Fatalf("identity/schema/ID precision lost: %s", body)
	}
	if b.StartedAt.IsZero() || b.CompletedAt.Before(b.StartedAt) || b.Evidence.CollectedAt.Before(b.StartedAt) || b.Context.CollectedAt.Before(b.Evidence.CollectedAt) || b.Health.CollectedAt.Before(b.Context.CollectedAt) || b.CompletedAt.Before(b.Health.CollectedAt) || !b.Context.Data.AsOf.Equal(snapshot.Context.Data.AsOf) || !b.Health.Data.GeneratedAt.Equal(snapshot.Health.Data.GeneratedAt) {
		t.Fatalf("capture/source clocks lost")
	}
	if !b.Coverage.EvidenceHistoryTruncated || !b.Coverage.RelatedResourcesTruncated || !b.Coverage.RelatedTimelineTruncated || !slices.Contains(b.Coverage.UnavailableSources, "evidence.prometheus_alert_metadata") || len(b.Coverage.UnhealthySignals) != 0 || b.Coverage.AlertCollectionState != "current" || len(b.Limitations) == 0 || b.Redaction.Profile != "operator-metadata-only" {
		t.Fatalf("missing coverage: %+v", b.Coverage)
	}
	for _, secret := range []string{"private-secret", "unexpected_raw_payload", "explicit-token", "environment-token", srv.URL} {
		if bytes.Contains(body, []byte(secret)) || strings.Contains(out.String()+stderr.String(), secret) {
			t.Errorf("artifact/receipt contains %q", secret)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("owner-only file: %v %v", info, err)
	}
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || string(receipt["bytes"]) != fmt.Sprint(len(body)) || string(receipt["path"]) != strconv.Quote(path) {
		t.Fatalf("receipt=%s err=%v", out.String(), err)
	}
	assertObsBundleNoTemps(t, filepath.Dir(path))
}

func TestCmdObsBundleStdoutPreservesUnknownScopeAndHealthGaps(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			out, stderr := obsBundleTestStreams(t)
			b := obsBundleTestSnapshot(obsBundleTestRef, 25, 25)
			b.Context.Data.Scope = api.ObsIncidentContextScope{State: "unknown", Reason: "no_verified_resource_reference"}
			b.Context.Data.Counts = nil
			b.Context.Data.UnavailableSources = []string{"incident_scope"}
			b.Health.Data.PrometheusAvailable = false
			b.Health.Data.Signals["alerts_firing"] = api.ObsHealthSignal{State: api.ObsHealthSignalStale, Reason: "observation_stale"}
			b.Health.Data.AlertHistory = &api.ObsAlertCollectionHealth{State: "unknown", CollectionState: "failed", Reason: "collection_failed"}
			if legacy {
				b.Health.Data.Signals, b.Health.Data.AlertHistory, b.Health.Data.AlertRetention = nil, nil, nil
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { obsBundleTestSection(w, r, b) }))
			defer srv.Close()
			t.Setenv("FAAS_APID_URL", srv.URL)
			t.Setenv("FAAS_ADMIN_TOKEN", "token")
			if code := cmdObsBundle([]string{"--incident", obsBundleTestRef, "--output", "-", "--json"}); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			var got obsIncidentBundle
			wantCollection := "failed"
			if legacy {
				wantCollection = "unknown"
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Context.Data.Counts != nil || got.Coverage.RelationshipScope != "unknown" || !slices.Contains(got.Coverage.UnavailableSources, "context.incident_scope") || !slices.Contains(got.Coverage.UnhealthySignals, "alerts_firing") || got.Coverage.AlertCollectionState != wantCollection {
				t.Fatalf("unknown evidence became healthy/zero: %s err=%v", out.String(), err)
			}
			if legacy && (len(got.Coverage.MissingHealthMetadata) != 6 || got.Coverage.RuleHealthState != "unknown") {
				t.Fatalf("legacy gaps hidden: %+v", got.Coverage)
			}
		})
	}
}

func TestCmdObsBundlePinsAlertSeriesToCanonicalEpisode(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	series, episode := strings.Repeat("a", 64), strings.Repeat("b", 64)
	b := obsBundleTestSnapshot("platform_alert/"+episode, 25, 25)
	b.Evidence.Data.Incident.ResourceID = series
	b.Evidence.Data.Incident.AlertEpisode = &api.ObsAlertEpisode{SeriesID: series}
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		obsBundleTestSection(w, r, b)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsBundle([]string{"--incident", "platform_alert/" + series, "--output", "-"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	var got obsIncidentBundle
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.IncidentID != "platform_alert/"+episode || got.RequestedIncidentID != "platform_alert/"+series || len(paths) != 3 || paths[1] != "/v1/admin/obs/incidents/platform_alert/"+episode+"/context" {
		t.Fatalf("episode not pinned: %s paths=%v err=%v", out.String(), paths, err)
	}
}
