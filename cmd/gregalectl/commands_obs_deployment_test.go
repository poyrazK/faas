package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentSnapshotTest(now time.Time) obsDeploymentSnapshot {
	serving := servingCLIResponse()
	run := now.Add(-time.Minute)
	serving.GeneratedAt, serving.Inventory.ObservedAt = now, &now
	serving.Probes[0].EvaluatedAt, serving.Probes[0].LastRunAt = &now, &run
	notification := obsReceiverCLIResponse(now)
	notification.AlertmanagerAPI.ObservedAt = &now
	known := func(value float64, window time.Duration) api.ObsNotificationSignal {
		return api.ObsNotificationSignal{State: "healthy", Value: &value, ObservedAt: &now, WindowSeconds: int(window.Seconds()), MaxAgeSeconds: 120}
	}
	for i, name := range deploymentSignalNames() {
		window, value := time.Duration(0), 0.0
		if i >= 4 {
			window = api.ObsNotificationWindow
		}
		if i < 2 {
			value = 1
		}
		if name == "prometheus_queue_capacity" {
			value = 1000
		}
		notification.Signals[name] = known(value, window)
	}
	notification.Integrations = []api.ObsNotificationIntegration{{Integration: "slack", Attempts: known(0, api.ObsNotificationWindow), Failures: known(0, api.ObsNotificationWindow)}}
	// A quiet route may have a historical confirmation. A fresh observation of
	// that clock is sufficient; verification must not send a notification.
	*notification.ReceiverHealth.Groups[0].LastSuccessObservation.Value = float64(now.Add(-24 * time.Hour).Unix())
	return obsDeploymentSnapshot{serving: obsDeploymentServingRead{value: serving}, notifications: notification}
}

func deploymentProbeTestState(snapshot *obsDeploymentSnapshot, state, reason string) {
	probe := &snapshot.serving.value.Probes[0]
	probe.State, probe.Reason = state, reason
	if state == "missing" {
		no := false
		probe.Reporting, probe.LastRunAt = &no, nil
	}
	counts := operatorobs.ServingProbeCounts(snapshot.serving.value.Probes)
	snapshot.serving.value.Counts = &counts
	snapshot.serving.value.State, snapshot.serving.value.Reason = operatorobs.ServingProbeOverall(counts)
}

func TestObsDeploymentAssessment(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	for _, tc := range []struct {
		name   string
		mutate func(*obsDeploymentSnapshot)
		states [3]string
	}{
		{"ready with historical receiver success", func(*obsDeploymentSnapshot) {}, [3]string{"passed", "passed", "passed"}},
		{"pre-rollout run", func(s *obsDeploymentSnapshot) {
			old := after.Add(-time.Second)
			s.serving.value.Probes[0].LastRunAt = &old
		}, [3]string{"unknown", "passed", "passed"}},
		{"fresh missing probe", func(s *obsDeploymentSnapshot) { deploymentProbeTestState(s, "missing", "result_missing") }, [3]string{"degraded", "passed", "passed"}},
		{"stale snapshot", func(s *obsDeploymentSnapshot) { s.serving.value.GeneratedAt = now.Add(-3 * time.Minute) }, [3]string{"unknown", "passed", "passed"}},
		{"unsafe node", func(s *obsDeploymentSnapshot) { s.serving.value.Probes[0].Node = "https://private/token" }, [3]string{"unknown", "passed", "passed"}},
		{"absent receiver endpoint field", func(s *obsDeploymentSnapshot) { s.notifications.ReceiverHealth = nil }, [3]string{"passed", "passed", "unknown"}},
		{"cold receiver", func(s *obsDeploymentSnapshot) {
			out := s.notifications.ReceiverHealth
			out.State, out.Reason = "partial", "incomplete_evidence"
			group := &out.Groups[0]
			group.State, group.Reason = "partial", "incomplete_evidence"
			group.LastSuccessObservation.State, group.LastSuccessObservation.Reason, group.LastSuccessObservation.Value = "no_samples", "success_not_observed", nil
		}, [3]string{"passed", "passed", "unknown"}},
		{"pre-rollout source observation", func(s *obsDeploymentSnapshot) {
			old := after.Add(-time.Second)
			s.notifications.ReceiverHealth.Inventory.ObservedAt = &old
		}, [3]string{"passed", "passed", "unknown"}},
		{"pipeline fault and unavailable probe", func(s *obsDeploymentSnapshot) {
			s.serving.err = errors.New("private provider text")
			*s.notifications.Signals["alertmanager_failures"].Value = 1
		}, [3]string{"unknown", "degraded", "passed"}},
		{"queue backlog", func(s *obsDeploymentSnapshot) { *s.notifications.Signals["prometheus_queue_length"].Value = 801 }, [3]string{"passed", "degraded", "passed"}},
		{"unknown integration", func(s *obsDeploymentSnapshot) { s.notifications.Integrations[0].Integration = "private-address" }, [3]string{"passed", "unknown", "passed"}},
		{"unsafe source reason", func(s *obsDeploymentSnapshot) {
			signal := s.notifications.Signals["alertmanager_attempts"]
			signal.State, signal.Reason, signal.Value = "unavailable", "https://private/token", nil
			s.notifications.Signals["alertmanager_attempts"] = signal
		}, [3]string{"passed", "unknown", "passed"}},
		{"unreviewed root strings are excluded", func(s *obsDeploymentSnapshot) {
			s.notifications.State = "private-provider-error"
			s.notifications.Limitations = []string{"private-provider-error"}
			s.notifications.AlertmanagerAPI.Reason = "private-provider-error"
		}, [3]string{"passed", "passed", "passed"}},
		{"failed receiver and unknown intent", func(s *obsDeploymentSnapshot) {
			out := s.notifications.ReceiverHealth
			out.State, out.Reason = "degraded", "requests_failing"
			out.Inventory.State, out.Inventory.Reason, out.Inventory.Value = "unavailable", "inventory_missing", nil
			group := &out.Groups[0]
			group.Expected, group.State, group.Reason = nil, "degraded", "requests_failing"
			*group.Failures.Value = 1
		}, [3]string{"passed", "passed", "degraded"}},
		{"notification unavailable", func(s *obsDeploymentSnapshot) { s.notificationErr = errors.New("https://private/token") }, [3]string{"passed", "unknown", "unknown"}},
		{"unconfigured serving", func(s *obsDeploymentSnapshot) {
			out := &s.serving.value
			no, zero := false, 0
			out.Inventory.Configured, out.Inventory.ExpectedPairs = &no, &zero
			out.State, out.Reason, out.Counts, out.Probes = "partial", "inventory_not_configured", nil, []api.ObsServingProbe{}
		}, [3]string{"unknown", "passed", "passed"}},
		{"explicitly empty serving", func(s *obsDeploymentSnapshot) {
			out := &s.serving.value
			zero := 0
			out.Inventory.ExpectedPairs, out.Probes = &zero, []api.ObsServingProbe{}
			counts := operatorobs.ServingProbeCounts(out.Probes)
			out.Counts = &counts
			out.State, out.Reason = operatorobs.ServingProbeOverall(counts)
		}, [3]string{"unknown", "passed", "passed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := deploymentSnapshotTest(now)
			tc.mutate(&snapshot)
			checks, serving, notification := assessObsDeployment(snapshot, after, now)
			for i, want := range tc.states {
				if checks[i].State != want {
					t.Fatalf("check %s = %+v, want %s", checks[i].Name, checks[i], want)
				}
			}
			body, err := json.Marshal([]any{checks, serving, notification})
			if err != nil || strings.Contains(string(body), "private") {
				t.Fatalf("unsafe report projection: %s, %v", body, err)
			}
		})
	}
}

func TestObsDeploymentWaitAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		code int
		want string
		had  bool
	}{
		{"recovery", "recover", 0, "passed", true},
		{"confirmed failure at deadline", "fault", 1, "degraded", true},
		{"incomplete at deadline", "unknown", 3, "unknown", false},
		{"lost evidence after fault", "lost", 3, "unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: 2 * time.Second, poll: time.Second}
			calls, saved := 0, 0
			fetch := func(context.Context) obsDeploymentSnapshot {
				calls++
				s := deploymentSnapshotTest(now)
				if tc.mode == "fault" || (tc.mode == "recover" || tc.mode == "lost") && calls == 1 {
					deploymentProbeTestState(&s, "missing", "result_missing")
				}
				if tc.mode == "unknown" || tc.mode == "lost" && calls > 1 {
					s.serving.err = errors.New("unavailable")
				}
				return s
			}
			wait := func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }
			report, code := runObsDeploymentVerification(context.Background(), opts, fetch, func() time.Time { return now }, wait, func(report obsDeploymentReport) error {
				if saved == 0 && (calls != 0 || report.Attempts != 0 || report.State != "unknown") {
					t.Fatal("previous evidence was not invalidated before network reads")
				}
				saved++
				return nil
			})
			if code != tc.code || report.State != tc.want || report.HadConfirmedFaults != tc.had || saved != calls+1 || calls < 2 || calls > 3 {
				t.Fatalf("report=%+v code=%d calls=%d saved=%d", report, code, calls, saved)
			}
		})
	}
}

func TestObsDeploymentServingRunUsesPolicyAge(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentSnapshotTest(now)
	run := now.Add(-5 * time.Minute)
	s.serving.value.Probes[0].LastRunAt = &run
	checks, _, _ := assessObsDeployment(s, now.Add(-10*time.Minute), now)
	if checks[0].State != "passed" {
		t.Fatal("a post-boundary run within its 900-second policy was treated as a stale source observation")
	}
	checks, _, _ = assessObsDeployment(s, now.Add(-4*time.Minute), now)
	if checks[0].State != "unknown" {
		t.Fatal("a fresh evaluation of a pre-boundary run passed")
	}
}

func TestObsDeploymentBudgetsCancellationAndReportFailure(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentSnapshotTest(now)
	if got := obsDeploymentPolicyWait(s.serving.value); got != 17*time.Minute {
		t.Fatalf("policy budget = %s", got)
	}
	s.serving.value.Probes[0].MaxResultAgeSeconds = 1e30
	if got := obsDeploymentPolicyWait(s.serving.value); got != time.Hour {
		t.Fatalf("uncapped policy budget = %s", got)
	}
	if got := obsDeploymentPolicyWait(api.ObsServingHealth{}); got != 20*time.Minute {
		t.Fatalf("fallback budget = %s", got)
	}
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), poll: time.Second}
	fetch := func(context.Context) obsDeploymentSnapshot { return deploymentSnapshotTest(now) }
	clock := func() time.Time { return now }
	noWait := func(context.Context, time.Duration) error { t.Fatal("unexpected wait"); return nil }
	report, code := runObsDeploymentVerification(context.Background(), opts, fetch, clock, noWait, func(obsDeploymentReport) error { return errors.New("private path") })
	if code != 2 || report.Reason != "report_write_failed" {
		t.Fatalf("report failure = %d %+v", code, report)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, code = runObsDeploymentVerification(ctx, opts, fetch, clock, noWait, func(obsDeploymentReport) error { return nil })
	if code != 3 || report.Attempts != 0 || report.Reason != "cancelled" {
		t.Fatalf("cancellation = %d %+v", code, report)
	}
	ctx, cancel = context.WithCancel(context.Background())
	report, code = runObsDeploymentVerification(ctx, opts, func(context.Context) obsDeploymentSnapshot {
		cancel()
		return deploymentSnapshotTest(now)
	}, clock, noWait, func(obsDeploymentReport) error { return nil })
	if code != 3 || report.State != "unknown" || report.Reason != "cancelled" {
		t.Fatalf("cancelled read passed: %d %+v", code, report)
	}
}

func TestCmdObsDeploymentUnavailableAndRedirects(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusNotFound, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			stdout, stderr := obsBundleTestStreams(t)
			var redirected atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/private-redirect" {
					redirected.Add(1)
				}
				if status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/private-redirect")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-provider-body"))
			}))
			defer srv.Close()
			t.Setenv("FAAS_APID_URL", srv.URL)
			args := []string{"--release", strings.Repeat("a", 40), "--after", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "--admin-token", "fixture-token", "--json", "--wait-timeout", "100ms"}
			if code := cmdObsVerifyDeployment(args); code != 3 || redirected.Load() != 0 {
				t.Fatalf("unavailable read: exit=%d redirects=%d", code, redirected.Load())
			}
			var report obsDeploymentReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.State != "unknown" || strings.Contains(stdout.String()+stderr.String(), "private") {
				t.Fatal("unavailable endpoint leaked text or produced invalid evidence")
			}
		})
	}
}

func TestObsDeploymentRejectsInvalidStdin(t *testing.T) {
	_, stderr := obsBundleTestStreams(t)
	original := os.Stdin
	defer func() { os.Stdin = original }()
	for _, token := range []string{"", "\n", "  \t\n", "token\r\n", "token\x00\n", "token-without-newline", strings.Repeat("x", api.ObsPostDeployTokenMaxBytes+1) + "\n"} {
		input, err := os.CreateTemp(t.TempDir(), "stdin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := input.WriteString(token); err != nil {
			t.Fatal(err)
		}
		if _, err := input.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		os.Stdin = input
		args := []string{"--release", strings.Repeat("a", 40), "--after", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "--token-stdin"}
		_, _, ok := parseObsDeploymentOptions(args)
		_ = input.Close()
		if ok || strings.Contains(stderr.String(), "token-without-newline") {
			t.Fatal("invalid stdin accepted or token printed")
		}
	}
}

func TestCmdObsVerifyDeploymentReadOnlyAndPrivateReport(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := deploymentSnapshotTest(now)
	stdout, stderr := obsBundleTestStreams(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer stdin-secret" {
			t.Error("verification changed the protected read contract")
		}
		switch r.URL.Path {
		case operatorobs.ServingHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.serving.value)
		case operatorobs.NotificationHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.notifications)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "ignored-env-secret")
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if _, err := stdin.WriteString("stdin-secret\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = original }()
	path := filepath.Join(t.TempDir(), "report.json")
	args := []string{subObsVerifyDeployment, "--release", strings.Repeat("a", 40), "--after", now.Add(-90 * time.Second).Format(time.RFC3339), "--token-stdin", "--json", "--report", path}
	if code := cmdObsDispatch(args); code != 0 {
		t.Fatalf("command exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report obsDeploymentReport
	if err := json.Unmarshal(body, &report); err != nil || report.State != "passed" || report.WaitSeconds != 1020 || report.Attempts != 1 {
		t.Fatalf("invalid report: %s %v", body, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || strings.Contains(string(body)+stdout.String()+stderr.String(), "secret") {
		t.Fatal("report permissions or credential redaction failed")
	}
}

func TestObsDeploymentInvalidOptions(t *testing.T) {
	_, _ = obsBundleTestStreams(t)
	base := []string{"--release", strings.Repeat("a", 40), "--after", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "--admin-token", "fixture"}
	for _, extra := range [][]string{{"--wait-timeout", "-1s"}, {"--wait-timeout", "61m"}, {"--poll-interval", "0s"}, {"--poll-interval", "61s"}, {"--release", "not-a-sha"}, {"--after", "tomorrow"}, {"--after", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, {"--token-stdin"}, {"extra"}} {
		args := append(append([]string{}, base...), extra...)
		if _, _, ok := parseObsDeploymentOptions(args); ok {
			t.Fatalf("invalid options accepted: %v", extra)
		}
	}
}
