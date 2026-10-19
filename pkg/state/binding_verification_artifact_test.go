package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

func reviewedSourceProbeDeployment(t *testing.T, dep Deployment, kind string) Deployment {
	t.Helper()
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	frozen.Source = &api.EnvironmentWorkloadSource{Kind: kind, Directory: "apps/" + dep.AppID}
	if kind == "dockerfile" {
		frozen.Source.Dockerfile = "Dockerfile"
	}
	if kind == "function" {
		frozen.AppType = AppTypeFunction
		frozen.RuntimeBase = "node22"
		frozen.Source.Runtime = "node22"
	}
	build, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	artifact := EnvironmentWorkloadSourceArtifact{RevisionID: frozen.RevisionID, CommitSHA: strings.Repeat("b", 40), DefinitionDigest: frozen.DefinitionDigest, BuildID: build.String(), Path: "/reviewed/" + dep.ID + ".tar.gz", SHA256: strings.Repeat("a", 64), Bytes: 256, LogPath: "/reviewed/" + dep.ID + ".log"}
	frozen.SourceArchive = &artifact
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	dep.EnvironmentWorkloadRuntime = string(raw)
	dep.Kind, dep.ImageDigest, dep.RootfsBytes = DeploymentKindGitHub, "", 4096
	dep.SourcePath, dep.SourceSHA256, dep.SourceBytes, dep.SourceRoot = artifact.Path, artifact.SHA256, artifact.Bytes, frozen.Source.Directory
	dep.BuildID, dep.LogPath, dep.CommitSHA = artifact.BuildID, artifact.LogPath, artifact.CommitSHA
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatal(err)
	}
	return dep
}

func TestManagedSourceProbeArtifactIdentity(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile", "function"} {
		t.Run(kind, func(t *testing.T) {
			m, app, dep, _, target := managedProbeFixture(t)
			dep = reviewedSourceProbeDeployment(t, dep, kind)
			target = reviewedSourceProbeDeployment(t, target, "dockerfile")
			m.deployments[dep.ID], m.deployments[target.ID] = dep, target
			spec, err := ResolveManagedServiceProbe(t.Context(), m, app.ID, dep.ID, "database")
			if err != nil {
				t.Fatal(err)
			}
			params := CreateAppTaskParams{AccountID: app.AccountID, AppID: app.ID, DeploymentID: dep.ID, Kind: AppTaskKindManual, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, spec.Service, spec.TargetDeploymentID}, RequireLiveDeployment: true, CreatedAt: time.Now().UTC(), TimeoutSeconds: 15, MaxOutputBytes: 4096, BindingVerification: &BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, TargetDeploymentID: spec.TargetDeploymentID, Revision: spec.ContextRevision, ManagedService: &spec}}
			task, err := m.CreateAppTask(t.Context(), params)
			if err != nil || task.ImageDigest != "" || task.ArtifactKey != dep.RootfsKey {
				t.Fatalf("source admission: %+v %v", task, err)
			}
			// A target's built output is part of the same caller evidence contract.
			target.RootfsBytes++
			m.deployments[target.ID] = target
			if !errors.Is(ManagedServiceProbeCurrent(t.Context(), m, task), ErrConflict) {
				t.Fatal("changed target build retained proof")
			}
			params.BindingVerification = nil
			params.RequireLiveDeployment = false
			params.Command = []string{"/bin/customer-command"}
			if _, err := m.CreateAppTask(t.Context(), params); !errors.Is(err, ErrAppTaskDeploymentUnavailable) {
				t.Fatalf("ordinary source task admitted: %v", err)
			}
		})
	}
}

func TestSourceVerificationRejectsArtifactDrift(t *testing.T) {
	for _, field := range []string{"rootfs", "rootfs-bytes", "archive", "checksum", "archive-bytes", "build", "log", "root", "commit", "kind", "fake-image", "missing-review", "held", "reviewed-rebuild"} {
		t.Run(field, func(t *testing.T) {
			m, app, dep, _ := managedQueueCanaryFixture(t)
			dep = reviewedSourceProbeDeployment(t, dep, "dockerfile")
			m.deployments[dep.ID] = dep
			spec, err := ResolveManagedServiceProbe(t.Context(), m, app.ID, dep.ID, "database")
			if err != nil {
				t.Fatal(err)
			}
			run := sourceVerificationRun(t, m, app, dep, spec)
			receipt, err := m.CreateQueueBindingCanary(t.Context(), app.AccountID, app.ID, api.CreateQueueBindingCanaryRequest{DeploymentID: dep.ID, Binding: "orders", TTLSeconds: 60, PublishConsent: true})
			if err != nil {
				t.Fatal(err)
			}
			// Model an exact handler acknowledgement, then ensure completed proof is
			// revoked as well as admission/dispatch when its immutable build changes.
			inv := m.invocations[receipt.ID]
			now := time.Now().UTC()
			inv.Attempts, inv.State, inv.ReceivedAt, inv.CompletedAt = 1, InvocationCompleted, &now, &now
			m.invocations[receipt.ID] = inv
			record := m.queueBindingCanaries[receipt.ID]
			record.CompletedDeploymentID, record.CompletedAttempt = dep.ID, 1
			m.queueBindingCanaries[receipt.ID] = record
			if got, err := m.GetQueueBindingCanary(t.Context(), app.AccountID, app.ID, receipt.ID); err != nil || got.Status != "succeeded" {
				t.Fatalf("completion: %+v %v", got, err)
			}
			switch field {
			case "rootfs":
				dep.RootfsKey += "changed"
			case "rootfs-bytes":
				dep.RootfsBytes = 0
			case "archive":
				dep.SourcePath += "changed"
			case "checksum":
				dep.SourceSHA256 = strings.Repeat("c", 64)
			case "archive-bytes":
				dep.SourceBytes++
			case "build":
				dep.BuildID = uuid.NewString()
			case "log":
				dep.LogPath += "changed"
			case "root":
				dep.SourceRoot = "other"
			case "commit":
				dep.CommitSHA = strings.Repeat("c", 40)
			case "kind":
				dep.Kind = DeploymentKindImage
			case "fake-image":
				dep.ImageDigest = "sha256:invented"
			case "missing-review":
				dep.EnvironmentWorkloadRuntime = ""
			case "held":
				dep.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(true)
			case "reviewed-rebuild":
				// Even self-consistent replacement provenance must not reuse old proof.
				frozen, _ := dep.ScopedWorkloadRuntime()
				build, _ := uuid.NewV7()
				frozen.SourceArchive.BuildID = build.String()
				dep.BuildID = build.String()
				raw, _ := json.Marshal(frozen)
				dep.EnvironmentWorkloadRuntime = string(raw)
			}
			m.deployments[dep.ID] = dep
			task := AppTask{AppID: app.ID, DeploymentID: dep.ID, BindingVerification: &BindingVerificationPin{ManagedService: &spec}}
			if ManagedServiceProbeCurrent(t.Context(), m, task) == nil {
				t.Fatal("artifact drift retained service proof")
			}
			if err := m.CheckBindingVerificationRun(t.Context(), app.AccountID, app.ID, run.ID); err == nil {
				t.Fatal("artifact drift retained durable authority")
			}
			if _, err := m.AdmitBindingVerificationRunStep(t.Context(), app.AccountID, app.ID, run.ID, 1); err == nil {
				t.Fatal("artifact drift admitted another message")
			}
			got, err := m.GetQueueBindingCanary(t.Context(), app.AccountID, app.ID, receipt.ID)
			if err != nil || got.Status != "blocked" {
				t.Fatalf("artifact drift retained queue proof: %+v %v", got, err)
			}
			got, err = m.CancelQueueBindingCanary(t.Context(), app.AccountID, app.ID, receipt.ID)
			if err != nil || got.Status != "cancelled" {
				t.Fatalf("stale cancellation: %+v %v", got, err)
			}
		})
	}
}

func sourceVerificationRun(t *testing.T, m *MemStore, app App, dep Deployment, spec api.ManagedServiceProbeSpec) api.BindingVerificationRun {
	t.Helper()
	policy, err := m.SetBindingReleasePolicy(t.Context(), app.AccountID, app.ID, dep.Scope, api.SetBindingReleasePolicyRequest{ExpectedRevision: new(int64), Mode: "enforce", QueueRequirements: map[string]string{"orders": api.QueueEvidenceHandlerCompletion}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkAppRuntimeConfigChanged(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	stamp, _, err := m.AppRuntimeConfigChangedAt(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	inventory := api.AppBindingInventory{App: app.Slug, Complete: true, RequestedDeploymentID: dep.ID, VerificationDeploymentID: dep.ID, VerificationScope: dep.Scope, RuntimeFreshness: &api.BindingRuntimeFreshness{ConfigChangedAt: &stamp}}
	report := api.BindingVerificationRun{ID: uuid.NewString(), App: app.Slug, DeploymentID: dep.ID, Scope: dep.Scope, CreatedAt: now, ExpiresAt: now.Add(time.Minute), Status: "pending", Policy: policy, Inventory: inventory, QueuePublish: true, ReviewDigest: bindingcheck.VerificationReviewDigest(inventory, policy), Steps: []api.BindingVerificationRunStep{
		{Plan: api.BindingVerificationPlanStep{Type: api.BindingTypeService, Binding: spec.Service, Name: spec.Service, Action: "probe"}},
		{Plan: api.BindingVerificationPlanStep{Type: api.BindingTypeQueue, Binding: "orders", Name: "orders", Action: "queue-canary", RequiredEvidence: api.QueueEvidenceHandlerCompletion}},
	}}
	params := &CreateAppTaskParams{AccountID: app.AccountID, AppID: app.ID, DeploymentID: dep.ID, Kind: AppTaskKindManual, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, spec.Service, spec.TargetDeploymentID}, RequireLiveDeployment: true, CreatedAt: now, TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, BindingVerification: &BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, TargetDeploymentID: spec.TargetDeploymentID, Revision: spec.ContextRevision, ManagedService: &spec}}
	run, err := m.CreateBindingVerificationRun(t.Context(), app.AccountID, app.ID, VerificationRunCreate{Report: report, Admissions: []VerificationRunAdmission{{Task: params}, {}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdmitBindingVerificationRunStep(t.Context(), app.AccountID, app.ID, run.ID, 0); err != nil {
		t.Fatal(err)
	}
	return run
}
