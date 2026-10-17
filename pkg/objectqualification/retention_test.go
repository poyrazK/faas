package objectqualification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

func TestQualificationCleanupReadinessRetriesTransientSamplingTimeout(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, healthyCleanupMetrics())
	}))
	defer server.Close()
	values, err := waitRecoveryCleanupMetrics(t.Context(), server.URL)
	if err != nil || attempts.Load() != 2 || values["candidates"] != 1 {
		t.Fatal("transient sample timeout became a failed readiness assertion", attempts.Load(), values, err)
	}
}

func TestQualificationCleanupReadinessPreservesFailuresAndDeadline(t *testing.T) {
	for _, fault := range []string{"missing", "failure", "removal", "deadline"} {
		t.Run(fault, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := healthyCleanupMetrics()
				switch fault {
				case "missing":
					body = strings.ReplaceAll(body, "faas_object_node_recovery_cleanup_candidates 1\n", "")
				case "failure":
					body = strings.ReplaceAll(body, "failures_total 0", "failures_total 1")
				case "removal":
					body = strings.ReplaceAll(body, "receipts_removed_total 0", "receipts_removed_total 1")
				case "deadline":
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), api.ObjectQualificationProbeTimeout)
			defer cancel()
			_, err := waitRecoveryCleanupMetrics(ctx, server.URL)
			if err == nil || fault == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lost failure or outer readiness deadline", fault, err)
			}
		})
	}
}

func healthyCleanupMetrics() string {
	return "faas_object_node_recovery_cleanup_enabled 1\n" +
		"faas_object_node_recovery_cleanup_ready 1\n" +
		"faas_object_node_recovery_cleanup_deferred 0\n" +
		"faas_object_node_recovery_cleanup_steps_total 1\n" +
		"faas_object_node_recovery_cleanup_candidates 1\n" +
		"faas_object_node_recovery_cleanup_failures_total 0\n" +
		"faas_object_node_recovery_cleanup_receipts_removed_total 0\n" +
		"faas_object_node_recovery_cleanup_uploads_removed_total 0\n"
}

func TestQualificationSettlementReplyLossIsSelectiveAndPreservesEvidence(t *testing.T) {
	id := objectrecovery.NewID()
	ack := objectrecovery.Acknowledgement{Version: 1, ID: id, Status: "committed", SettledAt: time.Now().UTC()}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := ack
		if r.Header.Get("Fixture") == "other" {
			value.ID = objectrecovery.NewID()
		}
		if r.Header.Get("Fixture") == "oversized" {
			_, _ = io.WriteString(w, strings.Repeat("x", api.ObjectNodeRecoveryResponseMaxBytes+1))
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer upstream.Close()
	p := newFaultProxy()
	defer p.server.Close()
	defer p.transport.CloseIdleConnections()
	p.target.Store(upstream.URL)
	o := &settlementObservation{id: id}
	o.drop.Store(true)
	p.settlementProbe.Store(o)
	for _, step := range []struct {
		fixture       string
		status, count int
		drop          bool
	}{{"other", 200, 0, true}, {"target", 502, 1, false}, {"target", 200, 2, false}, {"oversized", 502, 2, false}} {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, p.server.URL+"/?"+objectrecovery.Query+"=settle", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Fixture", step.fixture)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != step.status || readErr != nil {
			t.Fatal(step, response.StatusCode, readErr)
		}
		observed, count := o.snapshot()
		if count != step.count || o.drop.Load() != step.drop || count > 0 && !observed.SettledAt.Equal(ack.SettledAt) {
			t.Fatal("observation lost identity or timestamp", step, observed, count)
		}
		if step.fixture == "target" && step.status == 200 {
			var received objectrecovery.Acknowledgement
			if json.Unmarshal(body, &received) != nil || received.ID != id || !received.SettledAt.Equal(ack.SettledAt) {
				t.Fatal("observer changed provider reply bytes", string(body))
			}
		}
	}
	if p.puts.Load() != 0 {
		t.Fatal("settlement delivery replayed object bytes")
	}
}

func TestQualificationCleanupMetricsRemainSeparateFromJournalMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "faas_object_node_retention_journal_bytes 10\nfaas_object_node_retention_blob_bytes 20\nfaas_object_node_retention_pinned_records 1\nfaas_object_node_recovery_cleanup_ready 1\nfaas_object_node_recovery_cleanup_steps_total 7\n")
	}))
	defer server.Close()
	journal, err := retentionMetrics(t.Context(), server.URL)
	if err != nil || len(journal) != 3 || journal["journal_bytes"] != 10 {
		t.Fatal(journal, err)
	}
	cleanup, err := nodeMetrics(t.Context(), server.URL, "faas_object_node_recovery_cleanup_")
	if err != nil || len(cleanup) != 2 || cleanup["ready"] != 1 || cleanup["steps_total"] != 7 {
		t.Fatal(cleanup, err)
	}
}

func TestQualificationBuiltProcessesRecoveryRetention(t *testing.T) {
	daemon, control := os.Getenv("FAAS_OBJECT_QUALIFICATION_DAEMON"), os.Getenv("FAAS_OBJECT_QUALIFICATION_CTL")
	if daemon == "" || control == "" {
		t.Skip("set FAAS_OBJECT_QUALIFICATION_DAEMON and FAAS_OBJECT_QUALIFICATION_CTL to built executables")
	}
	o := DefaultOptions()
	o.Daemon, o.Control, o.Directory = daemon, control, filepath.Join(t.TempDir(), "qualification")
	o.Workers, o.Cycles, o.MaxWorkflows, o.Duration, o.ObjectBytes = 2, 1, 4, time.Second, 1024
	o.RecoveryRetention = true
	report, err := Run(t.Context(), o)
	if err != nil || !report.Passed || !report.Config.RecoveryRetention || len(report.Runs) != 2 {
		body, _ := json.MarshalIndent(report, "", "  ")
		t.Fatal(string(body), err)
	}
	for _, mode := range report.Runs {
		r := mode.RecoveryRetention
		if !mode.Passed || len(mode.Checks) != 16 || r == nil || !r.LostReplyRetried || r.AgedRetirementExercised || r.WindowSeconds != int64(api.ObjectNodeRecoveryWindow/time.Second) || len(r.Observations) != 4 {
			t.Fatal("format-5 qualification coverage incomplete", mode)
		}
		first := r.Observations[0].AcknowledgedAt
		for _, observation := range r.Observations {
			if first.IsZero() || !observation.AcknowledgedAt.Equal(first) || observation.CleanupSteps < 1 || observation.Candidates < 1 || observation.ReadyJobs != 0 || observation.WaitingJobs != observation.WaitingBefore {
				t.Fatal("lost protection across process recovery", observation)
			}
		}
		if r.Observations[len(r.Observations)-1].WaitingJobs < 1 {
			t.Fatal("unknown write lost its waiting settlement job")
		}
	}
}
