// adr: 1553 — bounded, deduplicated rollout outcomes share the wait evidence rules.
package state

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func rolloutNotificationFixture(now time.Time) (EnvironmentGitSource, EnvironmentDesiredRevision, *EnvironmentWorkloadActivationEvidence) {
	source := EnvironmentGitSource{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), EnvironmentID: uuid.NewString(), EnvironmentSlug: "production",
		Generation: 3, IntentVersion: 5, ApprovedRevisionID: uuid.NewString(), SourceCheckedAt: &now, SourceVerifiedAt: &now, SourceCommitSHA: strings.Repeat("b", 40),
		Spec: EnvironmentGitSourceSpec{Provider: "github", Repository: "shop/platform", RepositoryID: 42, InstallationID: 12,
			Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"}}
	revision := EnvironmentDesiredRevision{ID: source.ApprovedRevisionID, SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Digest: strings.Repeat("c", 64)}
	evidence := &EnvironmentWorkloadActivationEvidence{CommitSHA: revision.CommitSHA, GraphID: uuid.NewString(), SourceID: source.ID, EnvironmentID: source.EnvironmentID,
		RevisionID: revision.ID, DefinitionDigest: revision.Digest, PlanHash: strings.Repeat("d", 64), Generation: source.Generation, IntentVersion: source.IntentVersion,
		GraphPhase: "prepared", ArtifactsPrepared: true, Qualified: true, Activated: true, Serving: true, Candidates: 1,
		Workloads: []api.EnvironmentWorkloadProgress{{Resource: "workload/api", AppID: uuid.NewString(), ExecutionMode: "request",
			Preparation: "complete", Qualification: "complete", Activation: "complete", Serving: "complete"}}}
	return source, revision, evidence
}

func rolloutNotificationPayload(t *testing.T, events []environmentGitNotificationEvent, status string) api.EnvironmentGitOpsChangedWebhookPayload {
	t.Helper()
	var payload api.EnvironmentGitOpsChangedWebhookPayload
	if len(events) != 1 || json.Unmarshal(events[0].body, &payload) != nil || payload.Axis != "rollout" || payload.Status != status || payload.TransitionID != events[0].id {
		t.Fatalf("expected one %s outcome: %+v", status, events)
	}
	return payload
}

func TestGitOpsRolloutNotificationsProveExactCommitDeduplicateAndRecover(t *testing.T) {
	now := time.Now().UTC()
	source, revision, evidence := rolloutNotificationFixture(now)
	baseline, events, err := prepareEnvironmentGitRolloutNotifications(nil, source, revision, "shop", "", nil, now)
	if err != nil || len(events) != 0 || baseline["rollout"].Status != "progressing" {
		t.Fatal("approval must establish a quiet pending baseline", baseline, events, err)
	}
	evidence.GraphPhase, evidence.GraphErrorCode = "failed", "PRIVATE log and secret ref"
	evidence.Serving, evidence.Qualified, evidence.Activated = false, false, false
	evidence.Workloads[0].Preparation = "failed"
	evidence.Workloads[0].BlockingReasons = []string{"environment_workload_build_failed", "PRIVATE_SECRET"}
	next, events, err := prepareEnvironmentGitRolloutNotifications(baseline, source, revision, "shop", "run", evidence, now)
	if err != nil {
		t.Fatal(err)
	}
	failed := rolloutNotificationPayload(t, events, "failed")
	if failed.CommitSHA != revision.CommitSHA || failed.ApprovedRevision != revision.ID || failed.GraphID != evidence.GraphID || failed.DefinitionDigest != revision.Digest ||
		failed.ObservedVersion != source.IntentVersion || failed.RunID != "run" || len(failed.Diagnostics) == 0 || !strings.Contains(failed.DashboardPath, "/shop/") || strings.Contains(string(events[0].body), "PRIVATE") {
		t.Fatalf("unsafe or misattributed failure: %s", events[0].body)
	}
	if baseline["rollout"].FailedNotified {
		t.Fatal("preparing outcome mutated the committed baseline")
	}
	// JSON round-trip models restart. Missing evidence, provider polls and the
	// same failure must not reset the notification flags.
	encoded, _ := json.Marshal(next)
	var restarted environmentGitNotificationState
	if err := json.Unmarshal(encoded, &restarted); err != nil {
		t.Fatal(err)
	}
	for _, current := range []*EnvironmentWorkloadActivationEvidence{nil, evidence, nil, evidence} {
		restarted, events, err = prepareEnvironmentGitRolloutNotifications(restarted, source, revision, "shop", "run", current, now)
		if err != nil || len(events) != 0 {
			t.Fatal("failure repeated after missing evidence or restart", events, err)
		}
	}
	_, _, recovered := rolloutNotificationFixture(now)
	recovered.SourceID, recovered.EnvironmentID, recovered.RevisionID = source.ID, source.EnvironmentID, revision.ID
	// Normal intent/graph progress under the same approval is allowed.
	source.IntentVersion++
	recovered.IntentVersion = source.IntentVersion
	restarted, events, err = prepareEnvironmentGitRolloutNotifications(restarted, source, revision, "shop", "next-run", recovered, now)
	if err != nil {
		t.Fatal(err)
	}
	serving := rolloutNotificationPayload(t, events, "serving")
	if serving.PreviousStatus != "failed" || serving.CommitSHA == source.SourceCommitSHA || serving.ErrorCode != "" || len(serving.Diagnostics) != 0 || len(serving.BlockingReasons) != 0 {
		t.Fatal("recovery used polled ref or stale diagnostics", serving)
	}
	for i := 0; i < 3; i++ {
		restarted, events, err = prepareEnvironmentGitRolloutNotifications(restarted, source, revision, "shop", "next-run", recovered, now)
		if err != nil || len(events) != 0 {
			t.Fatal("serving outcome flapped", events, err)
		}
	}
}

func TestGitOpsRolloutNotificationsRejectUnprovenOutcomes(t *testing.T) {
	now := time.Now().UTC()
	for name, mutate := range map[string]func(*EnvironmentGitSource, *EnvironmentWorkloadActivationEvidence){
		"foreign source": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) { e.SourceID = uuid.NewString() },
		"old approval": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) {
			e.RevisionID = uuid.NewString()
		},
		"wrong digest": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) {
			e.DefinitionDigest = strings.Repeat("f", 64)
		},
		"old commit failure": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) {
			e.CommitSHA = strings.Repeat("e", 40)
			e.GraphPhase = "failed"
		},
		"old intent":     func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) { e.IntentVersion-- },
		"old generation": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) { e.Generation-- },
		"missing member": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) { e.Workloads = nil },
		"duplicate member": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) {
			e.Workloads = append(e.Workloads, e.Workloads[0])
		},
		"missing qualification": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) { e.Qualified = false },
		"activation only":       func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) { e.Serving = false },
		"missing scheduled occurrence": func(_ *EnvironmentGitSource, e *EnvironmentWorkloadActivationEvidence) {
			e.Workloads[0].ExecutionMode = "job"
			e.Workloads[0].Serving = "unknown"
		},
		"source stale": func(s *EnvironmentGitSource, _ *EnvironmentWorkloadActivationEvidence) {
			at := now.Add(-api.EnvironmentGitSourceStaleAfter)
			s.SourceVerifiedAt = &at
		},
		"source unverified": func(s *EnvironmentGitSource, _ *EnvironmentWorkloadActivationEvidence) { s.SourceVerifiedAt = nil },
		"source outage":     func(s *EnvironmentGitSource, _ *EnvironmentWorkloadActivationEvidence) { s.SourceErrorCode = "PRIVATE" },
		"report mode":       func(s *EnvironmentGitSource, _ *EnvironmentWorkloadActivationEvidence) { s.Spec.Mode = "report" },
	} {
		t.Run(name, func(t *testing.T) {
			source, revision, evidence := rolloutNotificationFixture(now)
			mutate(&source, evidence)
			_, events, err := prepareEnvironmentGitRolloutNotifications(nil, source, revision, "shop", "run", evidence, now)
			if err != nil || len(events) != 0 {
				t.Fatal("unproven evidence emitted outcome", events, err)
			}
		})
	}
}

func TestGitOpsRolloutNotificationsSupersedePendingApprovalOnly(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []string{"approval", "same commit new generation", "suspended", "report"} {
		t.Run(mode, func(t *testing.T) {
			source, revision, evidence := rolloutNotificationFixture(now)
			evidence.Serving, evidence.Workloads[0].Serving = false, "unknown"
			baseline, _, err := prepareEnvironmentGitRolloutNotifications(nil, source, revision, "shop", "run", evidence, now)
			if err != nil {
				t.Fatal(err)
			}
			old := source
			source.Generation++
			switch mode {
			case "approval":
				source.ApprovedRevisionID = uuid.NewString()
				revision.ID = source.ApprovedRevisionID
				revision.CommitSHA = strings.Repeat("e", 40)
			case "suspended":
				source.Suspended = true
			case "report":
				source.Spec.Mode = "report"
			}
			next, events, err := prepareEnvironmentGitRolloutNotifications(baseline, source, revision, "shop", "", nil, now)
			if err != nil {
				t.Fatal(err)
			}
			payload := rolloutNotificationPayload(t, events, "superseded")
			if payload.ApprovedRevision != old.ApprovedRevisionID || payload.CommitSHA != evidence.CommitSHA || payload.Generation != old.Generation || payload.ObservedVersion != old.IntentVersion || payload.GraphID != evidence.GraphID || payload.RunID != "" {
				t.Fatal("supersession was attributed to replacement revision", payload)
			}
			_, events, err = prepareEnvironmentGitRolloutNotifications(next, source, revision, "shop", "", nil, now)
			if err != nil || len(events) != 0 {
				t.Fatal("supersession repeated", events, err)
			}
		})
	}
}

func TestGitOpsRolloutNotificationBaselineAndDiagnosticsAreBounded(t *testing.T) {
	now := time.Now().UTC()
	source, revision, evidence := rolloutNotificationFixture(now)
	source.Generation, source.IntentVersion, evidence.Generation, evidence.IntentVersion = math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64
	evidence.GraphPhase = "failed"
	evidence.Qualified, evidence.Activated, evidence.Serving = false, false, false
	evidence.BlockingReasons = make([]string, api.EnvironmentGitOpsNotificationDetailsMax+1)
	for i := range evidence.BlockingReasons {
		evidence.BlockingReasons[i] = "PRIVATE"
	}
	// Distinct valid resource identities exercise the bound after safe mapping.
	for i := 0; i <= api.EnvironmentGitOpsNotificationDetailsMax; i++ {
		evidence.Workloads = append(evidence.Workloads, api.EnvironmentWorkloadProgress{Resource: "workload/a" + uuid.NewString(), BlockingReasons: []string{"PRIVATE"}})
	}
	next, events, err := prepareEnvironmentGitRolloutNotifications(environmentGitNotificationState{"source": {Status: "unavailable", Generation: math.MaxInt64}, "drift": {Status: "unavailable", Generation: math.MaxInt64}}, source, revision, "shop", "run", evidence, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := rolloutNotificationPayload(t, events, "failed")
	if !payload.DetailsTruncated || len(payload.Diagnostics) != api.EnvironmentGitOpsNotificationDetailsMax || strings.Contains(string(events[0].body), "PRIVATE") {
		t.Fatal("diagnostic bound or redaction failed", payload)
	}
	encoded, _ := json.Marshal(next)
	if len(encoded) >= api.EnvironmentGitOpsNotificationStateBytes || next["source"].Status != "unavailable" || next["drift"].Status != "unavailable" {
		t.Fatal("baseline exceeded existing schema bound or changed independent axes", len(encoded))
	}
}

func TestGitOpsRolloutReportMustMatchCurrentApprovalAndIntent(t *testing.T) {
	now := time.Now().UTC()
	source, revision, _ := rolloutNotificationFixture(now)
	valid := api.EnvironmentGitOpsPlan{Manager: source.ID, Revision: revision.ID, CommitSHA: revision.CommitSHA, Generation: source.Generation,
		DesiredDigest: revision.Digest, ObservedVersion: source.IntentVersion, Hash: strings.Repeat("d", 64)}
	if !environmentGitRolloutReportCurrent(source, revision, valid) {
		t.Fatal("current report rejected")
	}
	for name, mutate := range map[string]func(*api.EnvironmentGitOpsPlan){
		"source":     func(p *api.EnvironmentGitOpsPlan) { p.Manager = "foreign" },
		"revision":   func(p *api.EnvironmentGitOpsPlan) { p.Revision = "old" },
		"commit":     func(p *api.EnvironmentGitOpsPlan) { p.CommitSHA = strings.Repeat("e", 40) },
		"generation": func(p *api.EnvironmentGitOpsPlan) { p.Generation-- },
		"intent":     func(p *api.EnvironmentGitOpsPlan) { p.ObservedVersion-- },
		"digest":     func(p *api.EnvironmentGitOpsPlan) { p.DesiredDigest = strings.Repeat("e", 64) },
		"plan":       func(p *api.EnvironmentGitOpsPlan) { p.Hash = strings.Repeat("X", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			plan := valid
			mutate(&plan)
			if environmentGitRolloutReportCurrent(source, revision, plan) {
				t.Fatal("stale report accepted")
			}
		})
	}
}

func TestGitOpsRolloutNotificationsPinImmutableApprovalIdentity(t *testing.T) {
	now := time.Now().UTC()
	for _, field := range []string{"commit", "definition"} {
		t.Run(field, func(t *testing.T) {
			source, revision, evidence := rolloutNotificationFixture(now)
			before, _, err := prepareEnvironmentGitRolloutNotifications(nil, source, revision, "shop", "", nil, now)
			if err != nil {
				t.Fatal(err)
			}
			original, _ := json.Marshal(before)
			if field == "commit" {
				revision.CommitSHA = strings.Repeat("e", 40)
				evidence.CommitSHA = revision.CommitSHA
			} else {
				revision.Digest = strings.Repeat("e", 64)
				evidence.DefinitionDigest = revision.Digest
			}
			_, events, err := prepareEnvironmentGitRolloutNotifications(before, source, revision, "shop", "run", evidence, now)
			if !errors.Is(err, ErrConflict) || len(events) != 0 {
				t.Fatal("mutated immutable approval emitted outcome", events, err)
			}
			after, _ := json.Marshal(before)
			if string(original) != string(after) {
				t.Fatal("rejected observation changed committed baseline")
			}
		})
	}
}
