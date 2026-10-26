package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const obsWakeTestID = "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func obsWakeTestAPID(t *testing.T, status int) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	base := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(api.ObsWakeTimeline{WakeID: obsWakeTestID, Subject: "app-1", Events: []api.ObsWakeEvent{
			{At: base, Kind: "wake.admitted", Actor: "schedd", Data: json.RawMessage(`{"wake_id":"` + obsWakeTestID + `","node":"fsn-3"}`)},
			{At: base.Add(300 * time.Millisecond), Kind: "wake.restore_breakdown", Actor: "vmmd", Data: json.RawMessage(`{"wake_id":"` + obsWakeTestID + `","restore_ms":290}`)},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestCmdObsWakeMergesEventsAndLogs(t *testing.T) {
	obsLogsTestEnv(t)
	out, stderr := obsBundleTestStreams(t)
	apid, seen := obsWakeTestAPID(t, http.StatusOK)
	var lokiQuery string
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lokiQuery = r.URL.Query().Get("query")
		// 09:00:00.120 schedd and 09:00:00.350 vmmd, between and after the events.
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
			{"stream":{"host":"fsn-3","unit":"faas-vmmd.service"},"values":[["1792832400350000000","{\"level\":\"INFO\",\"msg\":\"guest ready\",\"wake_id\":\"` + obsWakeTestID + `\"}"]]},
			{"stream":{"host":"cp","unit":"faas-schedd.service"},"values":[["1792832400120000000","{\"level\":\"INFO\",\"msg\":\"placed\",\"node\":\"fsn-3\"}"]]}]}}`))
	}))
	defer loki.Close()
	t.Setenv("FAAS_APID_URL", apid.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "admin")
	t.Setenv("FAAS_LOKI_URL", loki.URL)

	// The wake_id may come before the flags.
	if code := cmdObsDispatch([]string{subObsWake, obsWakeTestID, "--log-margin", "1m"}); code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	r := (*seen)[0]
	if r.URL.Path != "/v1/admin/obs/wakes/"+obsWakeTestID || r.Header.Get("Authorization") != "Bearer admin" {
		t.Fatalf("apid request %s %v", r.URL, r.Header)
	}
	if lokiQuery != `{job="faas-journal"} |= "`+obsWakeTestID+`"` {
		t.Fatalf("loki query %q", lokiQuery)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{
		"wake " + obsWakeTestID + " subject=app-1 events=2 logs=2 span=350ms",
		"09:00:00.000          event wake.admitted actor=schedd node=fsn-3",
		"09:00:00.120   +120ms log   cp schedd INFO placed node=fsn-3",
		"09:00:00.300   +180ms event wake.restore_breakdown actor=vmmd restore_ms=290",
		"09:00:00.350    +50ms log   fsn-3 vmmd INFO guest ready wake_id=" + obsWakeTestID,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("timeline:\n%s\nwant:\n%s", out, strings.Join(want, "\n"))
	}
}

func TestCmdObsWakeWithoutLokiStillPrintsEvents(t *testing.T) {
	obsLogsTestEnv(t)
	out, stderr := obsBundleTestStreams(t)
	apid, _ := obsWakeTestAPID(t, http.StatusOK)
	t.Setenv("FAAS_APID_URL", apid.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "admin")
	if code := cmdObsWake([]string{obsWakeTestID}); code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if !strings.Contains(out.String(), "note: logs not searched: no Loki URL") || !strings.Contains(out.String(), "wake.restore_breakdown") {
		t.Fatalf("output:\n%s", out)
	}

	out.Reset()
	if code := cmdObsWake([]string{"--no-logs", "--json", obsWakeTestID}); code != 0 {
		t.Fatalf("json code %d: %s", code, stderr)
	}
	var doc struct {
		Wake     api.ObsWakeTimeline `json:"wake"`
		Timeline []obsWakeRow        `json:"timeline"`
		LogNote  string              `json:"log_note"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Timeline) != 2 || doc.Timeline[0].Source != "event" || doc.LogNote != "" {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestCmdObsWakeErrors(t *testing.T) {
	obsLogsTestEnv(t)
	_, stderr := obsBundleTestStreams(t)
	apid, seen := obsWakeTestAPID(t, http.StatusNotFound)
	t.Setenv("FAAS_APID_URL", apid.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "admin")
	if code := cmdObsWake([]string{obsWakeTestID}); code != 1 || !strings.Contains(stderr.String(), "no events are recorded for "+obsWakeTestID) {
		t.Fatalf("404: code %d: %s", code, stderr)
	}
	calls := len(*seen)
	for _, args := range [][]string{{}, {"short"}, {obsWakeTestID, "extra"}, {"--log-margin", "2h", obsWakeTestID}} {
		if code := cmdObsWake(args); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if code := cmdObsWake([]string{obsWakeTestID}); code != 2 {
		t.Errorf("no token: code %d", code)
	}
	if len(*seen) != calls {
		t.Fatalf("invalid input reached apid")
	}
}
