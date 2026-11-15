package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func servingRetryDiagnostics(probe api.ObsServingProbe) *api.ObsServingProbeDiagnostics {
	attempts, status, recovered := 2, 200, true
	return &api.ObsServingProbeDiagnostics{State: "available", EvaluatedAt: probe.EvaluatedAt, FailureCode: "none", FirstFailureCode: "tls", Attempts: &attempts, HTTPStatus: &status, RecoveredOnRetry: &recovered}
}

func TestObsServingDiagnosticsOptionalContractAndRendering(t *testing.T) {
	out := servingCLIResponse()
	var human strings.Builder
	writeObsServingHealth(&human, out)
	if !validObsServingHealth(out) || !strings.Contains(human.String(), "diagnostics=unknown reason=server_does_not_report_diagnostics") {
		t.Fatal("older servers no longer have an explicit unknown diagnostic fallback")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*api.ObsServingProbeDiagnostics)
		valid  bool
	}{
		{"retry recovered", func(*api.ObsServingProbeDiagnostics) {}, true},
		{"missing diagnostics", func(d *api.ObsServingProbeDiagnostics) {
			*d = api.ObsServingProbeDiagnostics{State: "unknown", Reason: "diagnostics_missing"}
		}, true},
		{"unknown with fabricated zero", func(d *api.ObsServingProbeDiagnostics) { d.State, d.Reason = "unknown", "diagnostics_missing" }, false},
		{"private failure", func(d *api.ObsServingProbeDiagnostics) { d.FailureCode = "https://private/token" }, false},
		{"private first failure", func(d *api.ObsServingProbeDiagnostics) { d.FirstFailureCode = "private-provider-error" }, false},
		{"private reason", func(d *api.ObsServingProbeDiagnostics) { d.Reason = "private-provider-error" }, false},
		{"missing attempts", func(d *api.ObsServingProbeDiagnostics) { d.Attempts = nil }, false},
		{"too many attempts", func(d *api.ObsServingProbeDiagnostics) { v := 3; d.Attempts = &v }, false},
		{"false recovery", func(d *api.ObsServingProbeDiagnostics) { v := false; d.RecoveredOnRetry = &v }, false},
		{"unobserved recovery", func(d *api.ObsServingProbeDiagnostics) { d.FirstFailureCode = "none" }, false},
		{"mixed evaluation", func(d *api.ObsServingProbeDiagnostics) { at := d.EvaluatedAt.Add(-time.Second); d.EvaluatedAt = &at }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := servingCLIResponse()
			out.Probes[0].Diagnostics = servingRetryDiagnostics(out.Probes[0])
			tc.mutate(out.Probes[0].Diagnostics)
			if got := validObsServingHealth(out); got != tc.valid {
				t.Fatalf("diagnostic contract accepted=%t, want %t", got, tc.valid)
			}
		})
	}
	out.Probes[0].Diagnostics = servingRetryDiagnostics(out.Probes[0])
	human.Reset()
	writeObsServingHealth(&human, out)
	for _, want := range []string{"diagnostics=available", "failure_code=none", "first_failure_code=tls", "attempts=2", "http_status=200", "recovered_on_retry=true"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human diagnostics missing %q: %s", want, &human)
		}
	}
}

func TestCmdObsServingDiagnosticsRejectsUnreviewedMetadata(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	out := servingCLIResponse()
	out.Probes[0].Diagnostics = servingRetryDiagnostics(out.Probes[0])
	out.Probes[0].Diagnostics.FirstFailureCode = "https://private.example/query-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(out) }))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "fixture")
	for _, args := range [][]string{nil, {"--json"}} {
		stdout.Reset()
		stderr.Reset()
		if code := cmdObsServingHealth(args); code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "query-secret") {
			t.Fatalf("unsafe diagnostics were emitted: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	}
}

func TestObsDeploymentRetainsServingRetryDiagnostics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := deploymentSnapshotTest(now)
	snapshot.serving.value.Probes[0].Diagnostics = servingRetryDiagnostics(snapshot.serving.value.Probes[0])
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: time.Second, poll: time.Second}
	report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot { return snapshot }, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("healthy probe unexpectedly waited"); return nil }, func(obsDeploymentReport) error { return nil })
	if code != 0 || report.State != "passed" || report.HadConfirmedFaults || report.Serving == nil {
		t.Fatalf("retry diagnostics changed the convergence gate: exit=%d %+v", code, report)
	}
	body, err := json.Marshal(report)
	if err != nil || !strings.Contains(string(body), `"first_failure_code":"tls"`) || !strings.Contains(string(body), `"recovered_on_retry":true`) {
		t.Fatalf("deployment report lost retry diagnostics: %s, %v", body, err)
	}
}

func TestObsDeploymentDiagnosticsMaximumReportEnvelope(t *testing.T) {
	now := time.Now().UTC()
	snapshot := deploymentSnapshotTest(now)
	serving := &snapshot.serving.value
	template := serving.Probes[0]
	template.Node = strings.Repeat("n", api.ObsServingProbeNodeMaxBytes)
	template.ExpectedFirstByteSeconds, template.MaxResultAgeSeconds = 2.123456789012345, 900.1234567890123
	template.Diagnostics = servingRetryDiagnostics(template)
	template.Diagnostics.FirstFailureCode = "body_mismatch"
	serving.Probes = make([]api.ObsServingProbe, api.ObsServingProbePairsMax)
	for i := range serving.Probes {
		serving.Probes[i] = template
		serving.Probes[i].Node = strings.Repeat("n", api.ObsServingProbeNodeMaxBytes-4) + fmt.Sprintf("%04d", i)
		serving.Probes[i].URLID = fmt.Sprintf("%064x", i)
	}
	channels := api.ObsServingProbeNotificationChannelsMax
	enabled := true
	lastAttempt, lastSuccess := now.Add(-24*time.Hour), now.Add(-48*time.Hour)
	serving.ProbeNotifications = &api.ObsServingProbeNotificationHealth{
		State: "available", ExpectedChannels: &channels, Limit: api.ObsServingProbeNotificationItemsMax,
		Counts:  &api.ObsServingProbeNotificationCounts{LastSubmissionFailed: channels},
		Omitted: channels - api.ObsServingProbeNotificationItemsMax, Truncated: true,
		Channels: make([]api.ObsServingProbeNotification, 0, api.ObsServingProbeNotificationItemsMax),
	}
	for i := range api.ObsServingProbeNotificationItemsMax {
		channel := "heartbeat"
		if i%2 == 1 {
			channel = "webhook"
		}
		serving.ProbeNotifications.Channels = append(serving.ProbeNotifications.Channels, api.ObsServingProbeNotification{
			Node: serving.Probes[i/2].Node, Channel: channel, State: "last_submission_failed", Enabled: &enabled,
			EvaluatedAt: &now, LastRunAt: template.LastRunAt, LastAttemptAt: &lastAttempt, LastSuccessAt: &lastSuccess, FailureCode: "http_status",
		})
	}
	expected := len(serving.Probes)
	serving.Inventory.ExpectedPairs = &expected
	counts := operatorobs.ServingProbeCounts(serving.Probes)
	serving.Counts = &counts
	receivers := snapshot.notifications.ReceiverHealth
	group := receivers.Groups[0]
	receivers.Groups = nil
	integrations := operatorobs.NotificationIntegrationNames()
	slices.Sort(integrations)
	integrationTemplate := snapshot.notifications.Integrations[0]
	snapshot.notifications.Integrations = nil
	for _, name := range integrations {
		copy := integrationTemplate
		copy.Integration = name
		snapshot.notifications.Integrations = append(snapshot.notifications.Integrations, copy)
	}
	for _, receiver := range operatorobs.NotificationReceiverNames() {
		for _, integration := range integrations {
			if len(receivers.Groups) == api.ObsNotificationIntegrationsMax {
				break
			}
			copy := group
			copy.Receiver, copy.Integration = receiver, integration
			receivers.Groups = append(receivers.Groups, copy)
		}
	}
	groupCount := float64(len(receivers.Groups))
	receivers.Inventory.Value = &groupCount
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-6 * time.Minute), wait: time.Minute, poll: time.Second}
	report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot { return snapshot }, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("unexpected wait"); return nil }, func(obsDeploymentReport) error { return nil })
	if code != 0 {
		t.Fatal("maximum valid serving response did not qualify")
	}
	recorder := newObsDeploymentTimeline(report.Checks)
	// A real full-fleet fault set supplies the retained preview and semantic
	// fingerprint. Retiming only its preview models later identical failures.
	initial := now.Add(-time.Duration(api.ObsPostDeployTransitionsMax) * time.Second)
	historical := *serving
	historical.ProbeNotifications = nil // The historical projection retains probe faults only.
	historical.GeneratedAt, historical.Inventory.ObservedAt = initial, &initial
	historical.Probes = slices.Clone(serving.Probes)
	diagnostic := *template.Diagnostics
	falseValue := false
	diagnostic.FailureCode, diagnostic.FirstFailureCode, diagnostic.RecoveredOnRetry, diagnostic.EvaluatedAt = "body_mismatch", "body_mismatch", &falseValue, &initial
	run := initial.Add(-time.Minute)
	for i := range historical.Probes {
		historical.Probes[i].State, historical.Probes[i].Reason = "failing", "probe_failing"
		historical.Probes[i].EvaluatedAt, historical.Probes[i].LastRunAt, historical.Probes[i].Diagnostics = &initial, &run, &diagnostic
	}
	historicalCounts := operatorobs.ServingProbeCounts(historical.Probes)
	historical.Counts = &historicalCounts
	historical.State, historical.Reason = operatorobs.ServingProbeOverall(historicalCounts)
	if !validObsServingHealth(historical) {
		t.Fatal("maximum historical serving fixture is invalid")
	}
	faultCheck := report.Checks[0]
	faultCheck.State, faultCheck.Reason = "degraded", "serving_probe_fault"
	faults := obsDeploymentProbeFailures([]obsDeploymentCheck{faultCheck}, &historical, opts.after, initial)
	if faults.total != api.ObsServingProbePairsMax {
		t.Fatal("maximum report fixture lost its full-fleet fault inventory")
	}
	for i := range api.ObsPostDeployTransitionsMax {
		check := report.Checks[0]
		check.State, check.Reason = "degraded", "serving_probe_fault"
		if i%2 != 0 {
			check.State, check.Reason = "unknown", "source_unavailable"
		}
		at := now.Add(time.Duration(i-api.ObsPostDeployTransitionsMax) * time.Second)
		current := faults
		current.preview = slices.Clone(faults.preview)
		for j := range current.preview {
			run := at.Add(-time.Minute)
			current.preview[j].EvaluatedAt, current.preview[j].LastRunAt, current.preview[j].Diagnostics.EvaluatedAt = at, &run, &at
		}
		notificationSnapshot := deploymentNotificationManyFaultsFixture(at, api.ObsNotificationIntegrationsMax)
		notificationChecks, _, notificationFaults := notificationFaultsForTest(notificationSnapshot, opts.after, at)
		notificationChecks[0] = check
		if i%2 != 0 {
			for j := 1; j < len(notificationChecks); j++ {
				notificationChecks[j].State, notificationChecks[j].Reason = "unknown", "source_server_error"
				status := 503
				notificationChecks[j].SourceHTTPStatus = &status
			}
		}
		recorder.observeWithFaults(notificationChecks, at, i+1, current, notificationFaults)
	}
	// Populate the independent notification history as well as the health
	// timeline and serving fault details, using a valid maximum-size preview.
	recorder.notifications = newObsDeploymentNotificationRecorder(opts.after)
	for i := range api.ObsPostDeployNotificationEventsMax + 100 {
		completed := lastAttempt.Add(time.Duration(i+1) * time.Nanosecond)
		serving.ProbeNotifications.Channels[i%len(serving.ProbeNotifications.Channels)].LastAttemptAt = &completed
		recorder.notifications.observe(serving, now, i+1)
	}
	report.Timeline = recorder.observe(report.Checks, now, api.ObsPostDeployTransitionsMax+1)
	report.StartedAt, report.HadConfirmedFaults = now.Add(-5*time.Minute), true
	report.Stability = &obsDeploymentStability{
		RequiredSeconds: 3600, State: "stabilizing", Reason: "awaiting_fresh_evidence", WindowStartedAt: &now, LastObservedAt: &now,
		SourceCount: 2139, Resets: 3600, LastResetAt: &now, LastResetReason: "evidence_set_changed",
	}
	evidence, valid := collectObsDeploymentStabilityEvidence(report.Serving, report.Notifications)
	if !valid {
		t.Fatal("maximum stability source vector is invalid")
	}
	old := obsDeploymentStabilityEvidence{}
	for key, at := range evidence {
		if key.budget > 0 {
			key.budget += 0.123456789012345
		}
		old[key] = at.Add(-time.Second)
	}
	report.Stability.Diagnostics = &obsDeploymentStabilityDiagnostics{
		State: "available", ObservationRemainingSeconds: 3600,
		Blockers:  obsDeploymentStabilityBlockers(evidence, evidence, evidence, time.Hour),
		LastReset: obsDeploymentStabilityResetDetails("evidence_set_changed", now, now.Add(-time.Second), old, old, evidence, nil),
	}
	report.State, report.Reason = "unknown", "awaiting_stability"
	report.Sources = maximumDeploymentSources(now)
	report.QualificationEvidence = maximumQualificationEvidence(now)
	report.QualificationEvidence.History = maximumQualificationHistory(now, report.QualificationEvidence.Checks)
	if history := report.QualificationEvidence.History; history.RetainedBytes < api.ObsPostDeployEvidenceHistoryBytesMax/2 || history.DroppedEvents == 0 || !history.Truncated {
		t.Fatal("maximum report did not populate bounded qualification change history")
	}
	if d := report.Stability.Diagnostics; len(d.Blockers.Items) != api.ObsPostDeployStabilityItemsMax || len(d.LastReset.Sources.Items) != api.ObsPostDeployStabilityItemsMax || !d.Blockers.Truncated || !d.LastReset.Sources.Truncated {
		t.Fatal("maximum report did not exercise both full stability previews")
	}
	for _, check := range report.QualificationEvidence.Checks {
		if check.LastBlocked == nil || len(check.LastBlocked.Blockers.Items) != api.ObsPostDeployEvidenceItemsMax || !check.LastBlocked.Blockers.Truncated {
			t.Fatal("maximum report did not populate all qualification previews")
		}
	}
	report.Limitations = append(report.Limitations, "stability_is_sampled_evidence_not_continuous_availability")
	report = fitObsDeploymentReport(report)
	if report.Timeline.ProbeEvidence.RetainedItems == 0 || !report.Timeline.ProbeEvidence.Truncated {
		t.Fatal("maximum report did not exercise bounded fault detail retention")
	}
	if evidence := report.Timeline.NotificationEvidence; evidence.RetainedItems == 0 || evidence.RetainedBytes < api.ObsPostDeployNotificationEvidenceBytesMax/2 || !evidence.Truncated {
		t.Fatal("maximum report did not populate independent primary notification evidence")
	}
	if advisory := report.Timeline.ProbeNotifications; advisory == nil && !slices.Contains(report.Limitations, obsDeploymentNotificationEnvelopeLimitation) || advisory != nil && (advisory.DroppedEvents == 0 || !advisory.HadSubmissionFailures) || recorder.notifications.snapshot().RetainedBytes < api.ObsPostDeployNotificationHistoryBytesMax/2 {
		t.Fatal("maximum report did not populate independent notification history")
	}
	response, err := json.Marshal(serving)
	if err != nil || len(response) > api.ObsIncidentBundleResponseMaxBytes {
		t.Fatalf("diagnostics exceeded the serving response envelope: %d bytes, %v", len(response), err)
	}
	body, err := json.MarshalIndent(report, "", "  ")
	t.Logf("maximum serving response=%d bytes, deployment report=%d bytes", len(response), len(body))
	if err != nil || len(body) > api.ObsIncidentBundleResponseMaxBytes {
		t.Fatalf("diagnostics exceeded the deployment report envelope: %d bytes, %v", len(body), err)
	}
	t.Run("degraded latest snapshot", func(t *testing.T) {
		for i := range serving.Probes {
			serving.Probes[i].State, serving.Probes[i].Reason = "failing", "probe_failing"
			diagnostic := *serving.Probes[i].Diagnostics
			no := false
			diagnostic.FailureCode, diagnostic.FirstFailureCode, diagnostic.RecoveredOnRetry = "body_mismatch", "body_mismatch", &no
			serving.Probes[i].Diagnostics = &diagnostic
		}
		counts := operatorobs.ServingProbeCounts(serving.Probes)
		serving.Counts = &counts
		serving.State, serving.Reason = operatorobs.ServingProbeOverall(counts)
		for i := range report.Notifications.Integrations {
			*report.Notifications.Integrations[i].Failures.Value = 1.5
		}
		receivers := report.Notifications.ReceiverHealth
		receivers.State, receivers.Reason = "degraded", "receiver_fault"
		for i := range receivers.Groups {
			receivers.Groups[i].State, receivers.Groups[i].Reason = "degraded", "requests_failing"
			*receivers.Groups[i].Failures.Value = 1.5
		}
		report.Checks[1].State, report.Checks[1].Reason = "degraded", "notification_integration_fault"
		report.Checks[2].State, report.Checks[2].Reason = "degraded", "receiver_requests_failing"
		report.Serving = serving
		report.Checks[0].State, report.Checks[0].Reason = "degraded", "serving_probe_fault"
		report.State, report.Reason = "degraded", "confirmed_monitoring_fault"
		if !validObsServingHealth(*serving) {
			t.Fatal("invalid maximum degraded serving fixture")
		}
		before := report
		report = fitObsDeploymentReport(report)
		if advisory := report.Timeline.ProbeNotifications; advisory == nil {
			if !slices.Contains(report.Limitations, obsDeploymentNotificationEnvelopeLimitation) {
				t.Fatal("maximum degraded report did not disclose omitted advisory history")
			}
		} else if advisory.EnvelopeOmittedEvents == 0 || !advisory.Truncated || len(before.Timeline.ProbeNotifications.Events) <= len(advisory.Events) {
			t.Fatal("maximum degraded report did not disclose trimmed advisory entries")
		}
		if report.State != before.State || report.Reason != before.Reason || report.Stability != before.Stability || report.Serving != before.Serving || report.Notifications != before.Notifications || len(report.Timeline.Transitions) != len(before.Timeline.Transitions) {
			t.Fatal("envelope pressure changed qualification, current sources or transitions")
		}
		if report.Timeline.ProbeEvidence.EnvelopeOmittedSnapshots+report.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots == 0 {
			t.Fatal("maximum degraded report did not disclose primary history omissions")
		}
		body, err := json.MarshalIndent(report, "", "  ")
		t.Logf("maximum degraded deployment report=%d bytes", len(body))
		if err != nil || len(body)+1 > api.ObsIncidentBundleResponseMaxBytes {
			t.Fatalf("advisory history exceeded the degraded report envelope: %d bytes, %v", len(body), err)
		}
	})
}
