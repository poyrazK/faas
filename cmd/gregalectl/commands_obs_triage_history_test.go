package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

// ADR-20261015173127929: CLI forwards bounded pagination; bundles capture one sanitized page.
func obsTriageHistoryBundleFixture() obsIncidentBundle {
	ref := "platform_alert/" + strings.Repeat("a", 64)
	b := obsBundleTestSnapshot(ref, 2, 1)
	b.Evidence.Data.Incident.Triage = &api.ObsAlertTriage{Revision: 3, Owned: true, OwnerAccountID: "11111111-1111-4111-8111-111111111111"}
	lo, hi := int64(1), int64(3)
	b.Evidence.Data.TriageHistory = &api.ObsAlertTriageHistory{
		IncidentID: ref, GeneratedAt: b.Evidence.Data.GeneratedAt, SnapshotAt: b.Evidence.Data.GeneratedAt, SnapshotRevision: 3, CurrentRevision: 3, Limit: 1, Truncated: true, NextCursor: "opaque-page", HistoryPath: operatorobs.TriageHistoryPath(strings.Repeat("a", 64)),
		Items:    []api.ObsAlertTriageHistoryEntry{{ID: "22222222-2222-4222-8222-222222222222", Revision: 3, Action: "claim", At: b.Evidence.Data.GeneratedAt, Owned: true, TraceID: strings.Repeat("d", 32), TracePath: "/v1/admin/obs/traces/" + strings.Repeat("d", 32)}},
		Coverage: api.ObsAlertTriageHistoryCoverage{State: "gaps", Reason: "missing_receipts", AvailableEntries: 2, MissingEntries: 1, OldestAvailableRevision: &lo, NewestAvailableRevision: &hi},
	}
	return b
}

func TestCmdObsTriageHistoryPagination(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	b := obsTriageHistoryBundleFixture()
	h := *b.Evidence.Data.TriageHistory
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != h.HistoryPath || r.URL.Query().Get("cursor") != "page+/=" || r.URL.Query().Get("limit") != "1" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(h)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	args := []string{subObsTriageHistory, "--incident", h.IncidentID, "--limit", "1", "--cursor", "page+/=", "--json"}
	if code := cmdObsDispatch(args); code != 0 {
		t.Fatalf("exit=%d %s", code, stderr)
	}
	var got api.ObsAlertTriageHistory
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.NextCursor != h.NextCursor || got.Items[0].Revision != 3 {
		t.Fatalf("json: %s %v", out, err)
	}
	out.Reset()
	args = args[:len(args)-1]
	if code := cmdObsDispatch(args); code != 0 || !strings.Contains(out.String(), "coverage=gaps available=2 missing=1") || !strings.Contains(out.String(), "revision=3 action=claim") || !strings.Contains(out.String(), "next_cursor=opaque-page") {
		t.Fatalf("human: %d %s %s", code, out, stderr)
	}
	for _, bad := range [][]string{{}, {"--incident", h.IncidentID, "--limit", "101"}, {"--incident", h.IncidentID, "--timeout", "0s"}, {"--incident", "deployment/11111111-1111-4111-8111-111111111111"}, {"--incident", h.IncidentID, "--cursor", strings.Repeat("x", api.ObsAlertTriageHistoryCursorMaxBytes+1)}} {
		if code := cmdObsTriageHistory(bad); code != 2 {
			t.Fatalf("invalid exit=%d", code)
		}
	}
}

func TestCmdObsBundleTriageHistoryCoverageAndRedaction(t *testing.T) {
	_, stderr := obsBundleTestStreams(t)
	b := obsTriageHistoryBundleFixture()
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		rec := httptest.NewRecorder()
		obsBundleTestSection(rec, r, b)
		var raw map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Error(err)
			return
		}
		if h, ok := raw["triage_history"].(map[string]any); ok {
			h["actor_email"] = "operator-secret"
			h["raw_payload"] = "private-secret"
			h["items"].([]any)[0].(map[string]any)["owner_account_id"] = "owner-secret"
		}
		_ = json.NewEncoder(w).Encode(raw)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	path := filepath.Join(t.TempDir(), "bundle.json")
	if code := cmdObsBundle([]string{"--incident", b.Evidence.Data.Incident.ID, "--limit", "2", "--history-limit", "1", "--output", path}); code != 0 {
		t.Fatalf("exit=%d %s", code, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got obsIncidentBundle
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	c := got.Coverage
	if reads != 3 || c.TriageHistoryState != "gaps" || !c.TriageHistoryTruncated || c.TriageHistoryMissingEntries == nil || *c.TriageHistoryMissingEntries != 1 {
		t.Fatalf("coverage/reads: %+v %d", c, reads)
	}
	for _, secret := range []string{"operator-secret", "private-secret", "owner-secret", "owner_account_id", b.Evidence.Data.Incident.Triage.OwnerAccountID} {
		if strings.Contains(string(body), secret) {
			t.Errorf("leaked %q", secret)
		}
	}
	// A server claiming inconsistent coverage must not produce an artifact.
	b.Evidence.Data.TriageHistory.CurrentRevision = 4
	badPath := filepath.Join(t.TempDir(), "bad.json")
	if code := cmdObsBundle([]string{"--incident", b.Evidence.Data.Incident.ID, "--limit", "2", "--history-limit", "1", "--output", badPath}); code == 0 {
		t.Fatal("inconsistent history accepted")
	}
	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Fatal("failure wrote artifact")
	}
	b.Evidence.Data.TriageHistory = nil
	summarizeObsBundle(&b)
	if b.Coverage.TriageHistoryState != "unknown" || b.Coverage.TriageHistoryMissingEntries != nil {
		t.Fatalf("old server: %+v", b.Coverage)
	}
}
