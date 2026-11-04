package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasehistory"
)

func TestReleaseHistoryListsActivations(t *testing.T) {
	started := time.Date(2026, 10, 28, 14, 30, 0, 0, time.UTC)
	finished := started.Add(95 * time.Second)
	records := []releasehistory.Record{
		{
			ID: "b", Activation: releasehistory.Activation{
				Host: "cp-1", Kind: "rollback", ReleaseID: strings.Repeat("a", 40), PreviousReleaseID: strings.Repeat("b", 40),
				Actor: "operator:ops", StartedAt: started.Add(time.Hour),
			},
			Status: "in_progress",
		},
		{
			ID: "a", Activation: releasehistory.Activation{
				Host: "cp-1", Kind: "deploy", ReleaseID: strings.Repeat("b", 40), CommitSHA: strings.Repeat("b", 40),
				Services: []string{"apid"}, Actor: "cd-controlplane run 7", StartedAt: started,
			},
			Status: "rolled_back", Detail: "restart apid: exit status 1", FinishedAt: &finished,
		},
	}
	var gotHost string
	var gotLimit int
	previous := releaseHistoryLister
	releaseHistoryLister = func(_ context.Context, host string, limit int) ([]releasehistory.Record, error) {
		gotHost, gotLimit = host, limit
		return records, nil
	}
	t.Cleanup(func() { releaseHistoryLister = previous })
	restoreOut := osStdout
	t.Cleanup(func() { osStdout = restoreOut })

	var out bytes.Buffer
	osStdout = &out
	if code := cmdReleaseHistory([]string{"--host", "cp-1", "--limit", "5"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if gotHost != "cp-1" || gotLimit != 5 {
		t.Fatalf("listed host %q limit %d, want cp-1 and 5", gotHost, gotLimit)
	}
	text := out.String()
	for _, want := range []string{
		"STARTED", "rollback", "in_progress", "aaaaaaaaaaaa  bbbbbbbbbbbb", "operator:ops",
		"rolled_back", "1m35s", "services: apid", "restart apid: exit status 1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("table lacks %q:\n%s", want, text)
		}
	}

	out.Reset()
	if code := cmdReleaseHistory([]string{"--json"}); code != 0 {
		t.Fatalf("--json exit = %d", code)
	}
	var rows []releaseHistoryRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if gotLimit != 20 || len(rows) != 2 || rows[1].Status != "rolled_back" || rows[1].FinishedAt == nil || rows[0].FinishedAt != nil {
		t.Fatalf("rows = %+v (default limit %d)", rows, gotLimit)
	}
}

func TestReleaseHistoryRejectsBadLimits(t *testing.T) {
	for _, args := range [][]string{{"--limit", "0"}, {"--limit", "1001"}, {"extra"}} {
		if code := cmdReleaseHistory(args); code != 1 {
			t.Errorf("%v: exit = %d, want 1", args, code)
		}
	}
}
