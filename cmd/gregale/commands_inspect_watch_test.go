package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func inspectWatchTestSummary(at time.Time) inspectSummary {
	return inspectSummary{SchemaVersion: inspectSummarySchemaVersion,
		App: inspectAppSummary{ID: inspectAppID, Slug: inspectSlug, Status: "active"},
		Operational: &api.AppOperationalSummary{Version: 1, AppID: inspectAppID, CheckedAt: at,
			Monitoring: api.AppOperationalMonitoring{Available: true, Status: "unknown", Reason: "insufficient_requests", Coverage: "observed_only", CheckedAt: &at, WindowStart: &at, WindowEnd: &at, IncidentsAvailable: true},
			Recovery: api.AppOperationalRecovery{RollbacksAvailable: true, RestartsAvailable: true,
				Rollbacks: []api.AppOperationalRollback{{ID: "rollback-a", Status: "blocked", UpdatedAt: at}, {ID: "rollback-b", Status: "running", UpdatedAt: at}},
				Restarts:  []api.RuntimeConfigRestartStatusResponse{{WakeID: "restart-a", Status: "retrying", Attempts: 1, FailureReason: "requests_active"}, {WakeID: "restart-b", Status: "queued"}}},
		}}
}

func TestInspectWatchChangesIgnoreClockChurnAndOrdering(t *testing.T) {
	before := inspectWatchTestSummary(time.Now().UTC())
	after := inspectWatchTestSummary(before.Operational.CheckedAt.Add(time.Minute))
	after.Operational.Recovery.Rollbacks[0], after.Operational.Recovery.Rollbacks[1] = after.Operational.Recovery.Rollbacks[1], after.Operational.Recovery.Rollbacks[0]
	after.Operational.Recovery.Restarts[0], after.Operational.Recovery.Restarts[1] = after.Operational.Recovery.Restarts[1], after.Operational.Recovery.Restarts[0]
	original, _ := json.Marshal(after)
	changed, err := inspectWatchChanges(&before, after)
	if err != nil || len(changed) != 0 {
		t.Fatalf("unchanged state emitted: %v %v", changed, err)
	}
	untouched, _ := json.Marshal(after)
	if string(original) != string(untouched) {
		t.Fatal("change detection mutated the observation")
	}
}

func TestInspectWatchChangesIncludeOperationalTransitions(t *testing.T) {
	cases := []struct {
		name   string
		change func(*inspectSummary)
	}{
		{"unknown health reason", func(s *inspectSummary) { s.Operational.Monitoring.Reason = "telemetry_unavailable" }},
		{"health", func(s *inspectSummary) { s.Operational.Monitoring.Status = "unhealthy" }},
		{"coverage", func(s *inspectSummary) { s.Operational.Monitoring.Coverage = "unavailable" }},
		{"incident", func(s *inspectSummary) {
			s.Operational.Monitoring.Incident = &api.AppOperationalIncident{ID: "incident-a"}
		}},
		{"restart progress", func(s *inspectSummary) { s.Operational.Recovery.Restarts[0].Status = "running" }},
		{"restart attempt", func(s *inspectSummary) { s.Operational.Recovery.Restarts[0].Attempts++ }},
		{"restart failure reason", func(s *inspectSummary) { s.Operational.Recovery.Restarts[0].FailureReason = "quiet_period_not_elapsed" }},
		{"rollback blocker", func(s *inspectSummary) { s.Operational.Recovery.Rollbacks[0].Code = "binding_verification_missing" }},
		{"recovery unavailable", func(s *inspectSummary) { s.Operational.Recovery.RestartsAvailable = false }},
		{"truncation", func(s *inspectSummary) { s.Operational.Recovery.RestartsTruncated = true }},
		{"operation removed", func(s *inspectSummary) { s.Operational.Recovery.Restarts = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := inspectWatchTestSummary(time.Now().UTC())
			after := inspectWatchTestSummary(before.Operational.CheckedAt)
			tc.change(&after)
			changed, err := inspectWatchChanges(&before, after)
			if err != nil || !reflect.DeepEqual(changed, []string{"current_operations"}) {
				t.Fatalf("lost transition: %v %v", changed, err)
			}
		})
	}
}

func TestRunInspectWatchSkipsQuietPollsAndStopsOnOutputFailure(t *testing.T) {
	stop := errors.New("output closed")
	polls := 0
	var events []inspectWatchEvent
	err := runInspectWatch(t.Context(), inspectSlug, time.Millisecond, func(ctx context.Context) (inspectSummary, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("read has no deadline")
		}
		polls++
		summary := inspectWatchTestSummary(time.Now().UTC())
		if polls > 2 {
			summary.Release = inspectReleaseSummary{Available: true, DeploymentID: "deployment-b", Status: "live"}
		}
		return summary, nil
	}, func(event inspectWatchEvent) error {
		events = append(events, event)
		if len(events) == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || polls != 3 || len(events) != 2 || events[0].Type != "snapshot" || events[1].Type != "change" || !reflect.DeepEqual(events[1].Changed, []string{"release"}) {
		t.Fatalf("watch drift: polls=%d events=%+v error=%v", polls, events, err)
	}
}

func TestRunInspectWatchUnavailableThenRestoredSnapshot(t *testing.T) {
	stop := errors.New("done")
	polls := 0
	var events []inspectWatchEvent
	err := runInspectWatch(t.Context(), inspectSlug, time.Millisecond, func(context.Context) (inspectSummary, error) {
		polls++
		if polls == 2 || polls == 3 {
			return inspectSummary{}, errors.New("PRIVATE_TRANSPORT_DETAILS")
		}
		return inspectWatchTestSummary(time.Now().UTC()), nil
	}, func(event inspectWatchEvent) error {
		events = append(events, event)
		if len(events) == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || polls != 4 || len(events) != 3 || events[1].Type != "unavailable" || events[1].Summary != nil || !strings.Contains(events[1].Message, "may be stale") || strings.Contains(events[1].Message, "PRIVATE") || events[2].Type != "snapshot" {
		t.Fatalf("unavailable state lost: %+v %v", events, err)
	}
}

func TestRunInspectWatchStopsOnAccessOrMissingApp(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			polls := 0
			problem := &api.APIError{Problem: api.Problem{Status: status}}
			err := runInspectWatch(t.Context(), inspectSlug, time.Millisecond, func(context.Context) (inspectSummary, error) {
				polls++
				return inspectSummary{}, problem
			}, func(inspectWatchEvent) error { t.Fatal("permanent failure retried"); return nil })
			if !errors.Is(err, problem) || polls != 1 {
				t.Fatalf("access failure did not stop: %v polls=%d", err, polls)
			}
		})
	}
	for _, status := range []int{408, 429, 500, 503} {
		if inspectWatchPermanentError(&api.APIError{Problem: api.Problem{Status: status}}) {
			t.Fatalf("retryable status %d treated as permanent", status)
		}
	}
}

func TestRunInspectWatchCancellationAndTimeout(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(map[bool]string{false: "between reads", true: "during read"}[inFlight], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			polls, events := 0, 0
			err := runInspectWatch(ctx, inspectSlug, time.Hour, func(readCtx context.Context) (inspectSummary, error) {
				polls++
				if inFlight {
					cancel()
					<-readCtx.Done()
					return inspectSummary{}, readCtx.Err()
				}
				return inspectWatchTestSummary(time.Now().UTC()), nil
			}, func(inspectWatchEvent) error { events++; cancel(); return nil })
			wantEvents := 1
			if inFlight {
				wantEvents = 0
			}
			if !errors.Is(err, context.Canceled) || inspectWatchExitCode(err) != 130 || polls != 1 || events != wantEvents {
				t.Fatalf("cancel: %v polls=%d events=%d", err, polls, events)
			}
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, _, restore := swapIO(t)
	defer restore()
	err := runInspectWatch(ctx, inspectSlug, time.Hour, func(context.Context) (inspectSummary, error) {
		return inspectWatchTestSummary(time.Now().UTC()), nil
	}, func(inspectWatchEvent) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) || inspectWatchExitCode(err) != 124 {
		t.Fatalf("duration limit: %v", err)
	}
}

func TestCmdInspectWatchReadsOnlyAndEmitsHumanOrJSONLines(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			base := newInspectSummaryServer(t, false)
			t.Cleanup(base.Close)
			var polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("watch issued mutation: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected mutation", 500)
					return
				}
				if r.URL.Path == "/v1/apps/"+inspectSlug {
					polls.Add(1)
				}
				if strings.HasSuffix(r.URL.Path, "/operational-summary") {
					summary := inspectWatchTestSummary(time.Now().UTC()).Operational
					summary.Recovery.Rollbacks = nil
					summary.Recovery.Restarts = summary.Recovery.Restarts[:1]
					if polls.Load() > 1 {
						summary.Monitoring.Status, summary.Monitoring.Reason = "unhealthy", "error_ratio"
						summary.Recovery.Restarts[0].Status = "running"
						summary.Recommendations = []api.AppOperationalRecommendation{{Code: "production_unhealthy", Severity: "error", Message: "Current routes need attention.", Next: "Inspect the current incident."}}
					}
					writeInspectSummaryJSON(t, w, summary)
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			configureInspectSummaryTest(t, server.URL)
			stdout, stderr, restore := swapIO(t)
			defer restore()
			args := []string{"inspect", "--watch", inspectSlug, "--interval", "1s", "--timeout", "2200ms"}
			if asJSON {
				args = append(args, "--json")
			}
			if exit := run(args); exit != 124 || polls.Load() < 2 || !strings.Contains(stderr(), "duration elapsed") {
				t.Fatalf("bounded watch exit=%d polls=%d: %s", exit, polls.Load(), stderr())
			}
			if asJSON {
				lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
				if len(lines) != 2 {
					t.Fatalf("watch emitted quiet polls or multi-line JSON: %s", stdout)
				}
				var first, second inspectWatchEvent
				if json.Unmarshal([]byte(lines[0]), &first) != nil || json.Unmarshal([]byte(lines[1]), &second) != nil || first.Type != "snapshot" || second.Type != "change" || first.SchemaVersion != 1 || first.Summary.SchemaVersion != inspectSummarySchemaVersion || first.ObservedAt.IsZero() {
					t.Fatalf("invalid JSON Lines events: %s", stdout)
				}
				if first.Summary.Operational.Monitoring.Status != "unknown" || first.Summary.Runtime.Health.Status != "verified" || second.Summary.Operational.Monitoring.Status != "unhealthy" || !reflect.DeepEqual(second.Changed, []string{"current_operations", "recommendations"}) {
					t.Fatalf("lost observed state or health distinction: %s", stdout)
				}
			} else {
				for _, want := range []string{"Current observation", "production routes: unknown", "coverage=observed_only", "Waiting for active requests to finish", "production routes: unhealthy", "Restart is being processed", "next: Inspect the current incident."} {
					if !strings.Contains(stdout.String(), want) {
						t.Errorf("missing %q: %s", want, stdout)
					}
				}
				if strings.Count(stdout.String(), "runtime:") != 1 || strings.Count(stdout.String(), "Current operations") != 2 {
					t.Fatalf("unchanged sections repeated: %s", stdout)
				}
			}
		})
	}
}

func TestCmdInspectWatchInvalidFlagsMakeNoRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(server.Close)
	configureInspectSummaryTest(t, server.URL)
	_, _, restore := swapIO(t)
	defer restore()
	for _, flags := range [][]string{
		{"--interval", "5s"}, {"--timeout", "0"}, {"--watch=false", "--interval", "5s"},
		{"--watch", "--upstreams"}, {"--watch", "--errors"}, {"--watch", "--scope", "preview"},
		{"--watch", "--interval", "0"}, {"--watch", "--interval", "999ms"}, {"--watch", "--interval", "2h"}, {"--watch", "--timeout=-1s"},
	} {
		if exit := cmdInspect(append([]string{inspectSlug}, flags...)); exit == 0 {
			t.Errorf("invalid flags accepted: %v", flags)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid flags reached server: %d", requests.Load())
	}
}

func TestEmitInspectWatchEventPropagatesWriteFailure(t *testing.T) {
	previous := osStdout
	osStdout = inspectWatchFailWriter{}
	t.Cleanup(func() { osStdout = previous; resetJSONOutput() })
	for _, asJSON := range []bool{false, true} {
		jsonOutput = asJSON
		if err := emitInspectWatchEvent(inspectWatchEvent{Type: "unavailable", Message: "Unavailable"}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write failure hidden (json=%v): %v", asJSON, err)
		}
	}
}

type inspectWatchFailWriter struct{}

func (inspectWatchFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
