package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCmdObsCollectionHistory(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	since := now.Add(-time.Hour).Format(time.RFC3339Nano)
	store := state.NewMemStore()
	response, err := operatorobs.AlertCollectionHistory(t.Context(), store, true, now.Add(-time.Hour), now, false)
	if err != nil {
		t.Fatal(err)
	}
	calls, mode := 0, "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.AlertCollectionHistoryPath || r.URL.Query().Get("since") != since || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("private upstream body"))
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	args := []string{"--since", since}
	if code := cmdObsDispatch(append([]string{subObsCollectionHistory}, args...)); code != 0 || !strings.Contains(stdout.String(), "collection_coverage=unknown") || !strings.Contains(stdout.String(), "unknown_microseconds=3600000000") {
		t.Fatalf("unknown: %d %s %s", code, stdout, stderr)
	}
	if err := store.RecordOperatorAlertFailure(t.Context(), now.Add(-time.Second), "timeout"); err != nil {
		t.Fatal(err)
	}
	response, err = operatorobs.AlertCollectionHistory(t.Context(), store, true, now.Add(-time.Hour), now, false)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := cmdObsCollectionHistory(append(append([]string{}, args...), "--json")); code != 0 {
		t.Fatalf("JSON: %d %s", code, stderr)
	}
	var got api.ObsAlertCollectionHistory
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Coverage.Failures != 1 || got.Coverage.FailedObservationMicroseconds != int64(time.Second/time.Microsecond) {
		t.Fatalf("failure coverage: %s %v", stdout, err)
	}
	before := calls
	for _, bad := range [][]string{{"--since", "bad"}, {"--timeout", "0s"}, {"extra"}, {"--limit", "5"}} {
		if code := cmdObsCollectionHistory(bad); code != 2 {
			t.Fatalf("bad arguments: %d", code)
		}
	}
	if calls != before {
		t.Fatal("invalid arguments sent requests")
	}
	stdout.Reset()
	response.Coverage.UnknownMicroseconds++
	if code := cmdObsCollectionHistory(args); code != 1 || stdout.Len() != 0 {
		t.Fatalf("bad partition accepted: %d %s", code, stdout)
	}
	mode = "error"
	stderr.Reset()
	if code := cmdObsCollectionHistory(args); code != 1 || strings.Contains(stderr.String(), "private upstream body") {
		t.Fatalf("redaction: %d %s", code, stderr)
	}
}
