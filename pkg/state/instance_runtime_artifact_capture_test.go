package state

// adr: 429. Captures bind real private producer stores, without native ACK claims.

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

type runtimeArtifactCaptureTestStore interface {
	runtimeArtifactInputTestStore
	InstanceApplicationStandardAdmissionStore
	ApplicationStandardEnrollmentStore
}

func createRuntimeArtifactCapture(t *testing.T, s runtimeArtifactCaptureTestStore, app App, dep Deployment) (Instance, InstanceApplicationStandardAdmission) {
	t.Helper()
	node, err := s.ComputeNodeByName(t.Context(), DefaultLocalNodeName)
	if errors.Is(err, ErrNotFound) {
		node, err = s.UpsertComputeNode(t.Context(), ComputeNode{Name: DefaultLocalNodeName, TargetURL: "unix:///tmp/producer-capture.sock", VPCPUs: 4, VCPUBudget: 4 * api.CPUOvercommit, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	}
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(StateColdBooting), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ins, capture
}

func runtimeArtifactCaptureRenewal(t *testing.T, s runtimeArtifactCaptureTestStore) {
	t.Helper()
	in, root, app, dep := artifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	_, before := createRuntimeArtifactCapture(t, s, app, dep)
	inputs, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || before.ArtifactInputHash != inputs.InputHash || !reflect.DeepEqual(before.RuntimeArtifacts, inputs.Artifacts) || before.RuntimeArtifacts[0].Bytes != root.Input.ArtifactBytes || root.Input.ArtifactBytes == root.Input.ContentBytes {
		t.Fatalf("capture lost complete producer identity: %v", err)
	}
	origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
	if err != nil {
		t.Fatal(err)
	}
	origin.Input.ID = uuid.NewString()
	approval, err := s.RecordDeploymentRegistryVerification(t.Context(), origin.Input)
	if err != nil {
		t.Fatal(err)
	}
	in.ID, in.RegistryVerificationID, in.RegistryInputHash = uuid.NewString(), approval.ID, approval.InputHash
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	_, after := createRuntimeArtifactCapture(t, s, app, dep)
	if before.ArtifactInputHash != after.ArtifactInputHash || !reflect.DeepEqual(before.RuntimeArtifacts, after.RuntimeArtifacts) || before.NativeInputHash == after.NativeInputHash {
		t.Fatal("producer identity changed on renewal or compatibility scan binding disappeared")
	}
	after.RuntimeArtifacts[0].StorageKey = "returned-mutation"
	again, err := s.GetInstanceApplicationStandardAdmission(t.Context(), after.InstanceID)
	if err != nil || again.ArtifactInputHash != before.ArtifactInputHash || !reflect.DeepEqual(again.RuntimeArtifacts, before.RuntimeArtifacts) {
		t.Fatalf("capture reader aliased immutable producer inputs: %v", err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatalf("producer metadata fabricated consumer observation: %v", err)
	}
}

func runtimeArtifactCaptureProducerReplacement(t *testing.T, s runtimeArtifactCaptureTestStore) {
	t.Helper()
	_, root, app, dep := artifactScanFixture(t, s, false)
	ins, before := createRuntimeArtifactCapture(t, s, app, dep)
	actual, err := s.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	replacement := root.Input
	replacement.ID = uuid.NewString()
	producer, err := s.PublishDeploymentRegistryRootfs(t.Context(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.DeploymentByID(t.Context(), dep.ID)
	if err != nil || actual.RootfsKey != same.RootfsKey || actual.RootfsPath != same.RootfsPath || actual.RootfsBytes != same.RootfsBytes {
		t.Fatalf("fixture changed compatibility metadata: %v", err)
	}
	if _, err := s.PublishInstanceRuntime(t.Context(), ins.ID, string(StateColdBooting), "stale-producer", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replacement producer reused a saved capture: %v", err)
	}
	_, after := createRuntimeArtifactCapture(t, s, app, dep)
	if before.ArtifactInputHash == after.ArtifactInputHash || before.NativeInputHash == after.NativeInputHash || after.RuntimeArtifacts[0].ProducerID != producer.ID {
		t.Fatal("same-path producer replacement retained native input identity")
	}
	saved, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil || !reflect.DeepEqual(saved.RuntimeArtifacts, before.RuntimeArtifacts) {
		t.Fatalf("stale capture history was backfilled: %v", err)
	}
}

func runtimeArtifactCaptureTwoDrives(t *testing.T, s runtimeArtifactCaptureTestStore) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), runtimeArtifactBaseScanInput(in, base)); err != nil {
		t.Fatal(err)
	}
	ins, capture := createRuntimeArtifactCapture(t, s, app, dep)
	inputs, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || capture.ArtifactInputHash != inputs.InputHash || !reflect.DeepEqual(capture.RuntimeArtifacts, inputs.Artifacts) || len(capture.RuntimeArtifacts) != 2 || capture.RuntimeArtifacts[0].Kind != "base-image" || capture.RuntimeArtifacts[1].BaseProducerID != base.ID {
		t.Fatalf("capture flattened or omitted a producer drive: %v", err)
	}
	replacement := base.Input
	replacement.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(t.Context(), ins.ID, string(StateColdBooting), "stale-base", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("current base change reused a historical native capture: %v", err)
	}
}

func completeRuntimeArtifactSidecar(t *testing.T, s runtimeArtifactCaptureTestStore, side DeploymentRegistryRootfs, app App, dep Deployment) DeploymentRegistryRootfs {
	t.Helper()
	origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, side.Input.RegistryVerificationID)
	if err != nil {
		t.Fatal(err)
	}
	main := cloneRegistryVerificationInput(origin.Input)
	main.ID, main.WorkloadName, main.ImageReference = uuid.NewString(), "", dep.ImageDigest
	main.SourceReference, main.SelectedReference = "registry.example/team/service@"+main.Proof.SubjectDigest, "registry.example/team/service@"+main.SelectedDigest
	proof, err := s.RecordDeploymentRegistryVerification(t.Context(), main)
	if err != nil {
		t.Fatal(err)
	}
	in := side.Input
	in.ID, in.WorkloadName, in.Kind, in.RootfsPath = uuid.NewString(), "", "full-rootfs", "/srv/main-capture.ext4"
	in.RegistryVerificationID, in.RegistryInputHash = proof.ID, proof.InputHash
	in.StorageKey, in.ArtifactDigest, in.ArtifactBytes = "apps/main-capture.ext4", imagechain.Digest([]byte("main capture output")), 8192
	root, err := s.PublishDeploymentRegistryRootfs(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runtimeArtifactCaptureSidecars(t *testing.T, s runtimeArtifactCaptureTestStore) {
	t.Helper()
	_, side, app, dep := artifactScanFixture(t, s, true)
	node, err := s.UpsertComputeNode(t.Context(), ComputeNode{Name: "sidecar-capture", TargetURL: "unix:///tmp/sidecar-capture.sock", VPCPUs: 4, VCPUBudget: 4 * api.CPUOvercommit, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(StateColdBooting), 128, node.ID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("sidecar alone acquired complete runtime identity: %v", err)
	}
	main := completeRuntimeArtifactSidecar(t, s, side, app, dep)
	ins, capture := createRuntimeArtifactCapture(t, s, app, dep)
	if len(capture.RuntimeArtifacts) != 2 || capture.RuntimeArtifacts[0].ProducerID != main.ID || capture.RuntimeArtifacts[1].ProducerID != side.ID || capture.RuntimeArtifacts[1].WorkloadName != "metrics" {
		t.Fatal("capture lost exact main and sidecar membership")
	}
	replacement := side.Input
	replacement.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(t.Context(), ins.ID, string(StateColdBooting), "stale-sidecar", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("same-path sidecar producer replacement reused capture: %v", err)
	}
}

func TestMemInstanceRuntimeArtifactCaptureRenewal(t *testing.T) {
	runtimeArtifactCaptureRenewal(t, NewMemStore())
}
func TestMemInstanceRuntimeArtifactCaptureProducerReplacement(t *testing.T) {
	runtimeArtifactCaptureProducerReplacement(t, NewMemStore())
}
func TestMemInstanceRuntimeArtifactCaptureTwoDrives(t *testing.T) {
	runtimeArtifactCaptureTwoDrives(t, NewMemStore())
}
func TestMemInstanceRuntimeArtifactCaptureSidecars(t *testing.T) {
	runtimeArtifactCaptureSidecars(t, NewMemStore())
}

func runtimeArtifactCaptureDoesNotBackfill(t *testing.T, s runtimeArtifactCaptureTestStore) {
	t.Helper()
	in, _, app, dep := registryRootfsFixture(t, s, false)
	if err := s.SetDeploymentRootfs(t.Context(), dep.ID, in.RootfsPath, in.StorageKey, in.ContentBytes); err != nil {
		t.Fatal(err)
	}
	ins, old := createRuntimeArtifactCapture(t, s, app, dep)
	if old.ArtifactInputHash != "" || len(old.RuntimeArtifacts) != 0 {
		t.Fatal("capture invented a producer before publication")
	}
	producer, err := s.PublishDeploymentRegistryRootfs(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil || saved.InputHash != old.InputHash || saved.ArtifactInputHash != "" || len(saved.RuntimeArtifacts) != 0 {
		t.Fatalf("producer publication manufactured historical native inputs: %v", err)
	}
	if _, err := s.PublishInstanceRuntime(t.Context(), ins.ID, string(StateColdBooting), "backfilled", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("capture without producer facts gained authority retroactively: %v", err)
	}
	_, fresh := createRuntimeArtifactCapture(t, s, app, dep)
	if fresh.ArtifactInputHash == "" || len(fresh.RuntimeArtifacts) != 1 || fresh.RuntimeArtifacts[0].ProducerID != producer.ID {
		t.Fatal("fresh capture omitted published producer")
	}
}

func TestMemInstanceRuntimeArtifactCaptureDoesNotBackfill(t *testing.T) {
	runtimeArtifactCaptureDoesNotBackfill(t, NewMemStore())
}

func TestInstanceRuntimeArtifactCaptureRejectsMalformedIdentity(t *testing.T) {
	s := NewMemStore()
	_, _, app, dep := artifactScanFixture(t, s, false)
	_, capture := createRuntimeArtifactCapture(t, s, app, dep)
	for _, change := range []struct {
		name string
		edit func(*deploymentRuntimeArtifactIdentity)
	}{
		{"format", func(in *deploymentRuntimeArtifactIdentity) { in.Format = "unknown" }},
		{"account", func(in *deploymentRuntimeArtifactIdentity) { in.AccountID = uuid.NewString() }},
		{"organization", func(in *deploymentRuntimeArtifactIdentity) { in.OrgID = uuid.NewString() }},
		{"application", func(in *deploymentRuntimeArtifactIdentity) { in.AppID = uuid.NewString() }},
		{"deployment", func(in *deploymentRuntimeArtifactIdentity) { in.DeploymentID = uuid.NewString() }},
		{"scope", func(in *deploymentRuntimeArtifactIdentity) { in.Scope = "sandbox" }},
		{"empty", func(in *deploymentRuntimeArtifactIdentity) { in.Artifacts = nil }},
		{"duplicate", func(in *deploymentRuntimeArtifactIdentity) { in.Artifacts = append(in.Artifacts, in.Artifacts[0]) }},
		{"producer hash", func(in *deploymentRuntimeArtifactIdentity) { in.Artifacts[0].ProducerHash = "" }},
		{"byte count", func(in *deploymentRuntimeArtifactIdentity) { in.Artifacts[0].Bytes = 0 }},
		{"missing base", func(in *deploymentRuntimeArtifactIdentity) {
			in.Artifacts[0].Kind, in.Artifacts[0].BaseProducerID, in.Artifacts[0].BaseInputHash = "app-layer", uuid.NewString(), capture.RuntimeArtifacts[0].ProducerHash
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			var input map[string]json.RawMessage
			if err := json.Unmarshal(capture.inputs, &input); err != nil {
				t.Fatal(err)
			}
			var identity deploymentRuntimeArtifactIdentity
			if err := json.Unmarshal(input["runtime_artifacts"], &identity); err != nil {
				t.Fatal(err)
			}
			change.edit(&identity)
			input["runtime_artifacts"], _ = json.Marshal(identity)
			raw, _ := json.Marshal(input)
			if _, err := decodeInstanceStandardAdmission(capture.InstanceID, raw, capture.CapturedAt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("malformed scoped producer identity decoded: %v", err)
			}
		})
	}
}
