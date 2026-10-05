// adr: 592
package sched

// adr: 435, 581. Decoded layer streams and cryptographic verification are real;
// artifact layout and scanner reports are explicit portable fixture facts.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/scanview"
	"github.com/onebox-faas/faas/pkg/state"
)

func composedWaveLayer(t *testing.T, index int, value string) (imagechain.Descriptor, string, imagechain.LayerConsumption) {
	t.Helper()
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	d := imagechain.Descriptor{Digest: imagechain.Digest(compressed.Bytes()), Size: int64(compressed.Len())}
	diff := imagechain.Digest([]byte(value))
	stream, err := imagechain.NewLayerStream(t.Context(), io.NopCloser(bytes.NewReader(compressed.Bytes())), d, diff, index)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := gzip.NewReader(stream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stream.VerifyingUncompressedReader(decoded)); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		t.Fatal(err)
	}
	c, err := stream.Consumption()
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	return d, diff, c
}

func composedWaveChain(t *testing.T, key string, layers ...string) state.BaseImageProducerInput {
	t.Helper()
	var descriptors []imagechain.Descriptor
	var diffs []string
	var consumed []imagechain.LayerConsumption
	for i, layer := range layers {
		d, diff, c := composedWaveLayer(t, i, layer)
		descriptors, diffs, consumed = append(descriptors, d), append(diffs, diff), append(consumed, c)
	}
	config := composedWaveJSON(t, map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": diffs}})
	manifest := composedWaveJSON(t, map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": imagechain.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, "layers": descriptors})
	digest := imagechain.Digest(manifest)
	artifact := []byte("portable declared artifact " + key)
	return state.BaseImageProducerInput{ID: uuid.NewString(), Artifact: imagechain.BaseArtifact{StorageKey: key, Digest: imagechain.Digest(artifact), Bytes: int64(len(artifact))}, SourceReference: "registry.example/base@" + digest, SourceDigest: digest, SelectedDigest: digest, ImageChain: &imagechain.Evidence{SourceManifest: manifest, Config: config}, LayoutVersion: imagechain.BaseLayoutVersion, GuestInitDigest: imagechain.Digest([]byte("init")), ContentBytes: 8, Layers: consumed}
}

func composedWaveReport(image, artifact string) *api.ScanResult {
	return &api.ScanResult{ImageDigest: image, ArtifactDigest: artifact, ScannerVersion: "0.116.0", ScannerDBStatus: "valid", ScannerDBVersion: "v6.0.2", ScannerDBBuiltAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), Vulnerabilities: []api.Vulnerability{}}
}

func (f *composedWaveFixture) publishBase(t *testing.T) {
	t.Helper()
	base, err := f.s.PublishBaseImageProducer(t.Context(), composedWaveChain(t, baseKey(""), "parent"))
	if err != nil {
		t.Fatal(err)
	}
	f.base = base
	if _, err := f.s.PublishBaseImageScan(t.Context(), state.BaseImageScanInput{ID: uuid.NewString(), BaseProducerID: base.ID, BaseInputHash: base.InputHash, Artifact: base.Input.Artifact, SourceReference: base.Input.SourceReference, Status: "complete", ScannerName: "grype", Report: composedWaveReport(base.Input.SourceReference, base.Input.Artifact.Digest)}); err != nil {
		t.Fatal(err)
	}
}

type composedWaveSignaturePuller struct {
	digest     string
	attachment imagepublisher.ImageSignatureAttachment
}

func (p composedWaveSignaturePuller) ResolveDigest(context.Context, string) (string, error) {
	return p.digest, nil
}

func (p composedWaveSignaturePuller) FetchSignatureAttachments(context.Context, string, string) ([]imagepublisher.ImageSignatureAttachment, error) {
	return []imagepublisher.ImageSignatureAttachment{p.attachment}, nil
}

func (f *composedWaveFixture) verifyDeployment(t *testing.T, app state.App, dep state.Deployment, chain *imagechain.Evidence) state.DeploymentRegistryVerification {
	t.Helper()
	subject := imagechain.Digest(chain.SourceManifest)
	payload := []byte(fmt.Sprintf(`{"critical":{"identity":{"docker-reference":"registry.example/team/service"},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"}}`, subject))
	sum := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, f.key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	proof, err := imagepublisher.VerifyImageSignatureAttachments(t.Context(), composedWaveSignaturePuller{subject, imagepublisher.ImageSignatureAttachment{ManifestDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: fmt.Sprintf("sha256:%x", sum), Payload: payload, Signature: signature}}, dep.ImageDigest, []imagepublisher.TrustedPublisher{{Name: "company", PublicKey: &f.key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	reference := "registry.example/team/service@" + subject
	v, err := f.s.RecordDeploymentRegistryVerification(t.Context(), state.DeploymentRegistryVerificationInput{ID: uuid.NewString(), AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, ImageReference: dep.ImageDigest, SourceReference: reference, SelectedReference: reference, SelectedDigest: subject, Proof: proof, ImageChain: chain})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *composedWaveFixture) publishDeployment(t *testing.T, app state.App) state.Deployment {
	t.Helper()
	dep, err := f.s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "registry.example/team/service:latest"})
	if err != nil {
		t.Fatal(err)
	}
	chain := composedWaveChain(t, "base/unpublished-"+app.Slug+".ext4", "parent", app.Slug)
	proof := f.verifyDeployment(t, app, dep, chain.ImageChain)
	digest := imagechain.Digest([]byte("portable app output " + app.ID))
	root, err := f.s.PublishDeploymentRegistryRootfs(t.Context(), state.DeploymentRegistryRootfsInput{ID: uuid.NewString(), RegistryVerificationID: proof.ID, RegistryInputHash: proof.InputHash, AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Kind: "app-layer", StorageKey: "apps/" + app.Slug + "/" + dep.ID + ".ext4", RootfsPath: "/srv/" + dep.ID + ".ext4", ArtifactDigest: digest, ArtifactBytes: 10, ContentBytes: 8, LayerStart: 1, Layers: chain.Layers[1:], BaseProducerID: f.base.ID, BaseInputHash: f.base.InputHash})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PublishDeploymentArtifactScan(t.Context(), state.DeploymentArtifactScanInput{ID: uuid.NewString(), RootfsProducerID: root.ID, RootfsInputHash: root.InputHash, AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, ImageReference: dep.ImageDigest, ArtifactDigest: digest, ArtifactBytes: 10, Status: "complete", ScannerName: "grype", Report: composedWaveReport(dep.ImageDigest, digest)}); err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(app.ID).String()
	f.deps[id] = dep
	f.publishComposedScan(t, id, false)
	if err := f.s.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	dep, err = f.s.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

func (f *composedWaveFixture) publishComposedScan(t *testing.T, id string, failed bool) {
	t.Helper()
	app, dep := f.apps[id], f.deps[id]
	inputs, err := f.s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal("producer inputs", err)
	}
	var in state.DeploymentRuntimeScanInput
	if failed {
		in, err = state.NewFailedDeploymentRuntimeScanInput(uuid.NewString(), inputs, "scanner_invalid")
	} else {
		in = composedWaveScanInput(t, inputs)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PublishDeploymentRuntimeScan(t.Context(), in); err != nil {
		t.Fatal("publish composed scan", err)
	}
}

func composedWaveScanInput(t *testing.T, inputs state.DeploymentRuntimeProducerInputs) state.DeploymentRuntimeScanInput {
	t.Helper()
	sources := []runtimeadmission.ArtifactSource{}
	for _, a := range inputs.Artifacts {
		sources = append(sources, runtimeadmission.ArtifactSource{Kind: a.Kind, WorkloadName: a.WorkloadName, StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes})
	}
	hash, err := runtimeadmission.HashArtifactSources(sources)
	if err != nil {
		t.Fatal(err)
	}
	facts := runtimescan.Facts{Version: runtimescan.Version, InputHash: inputs.InputHash, SourcesHash: hash}
	reports := []state.DeploymentRuntimeScanReport{}
	for _, a := range inputs.Artifacts {
		if a.Kind == "base-image" {
			continue
		}
		tree := scanview.Tree{Version: scanview.Version, Digest: strings.Repeat("b", 64), ProjectionDigest: strings.Repeat("c", 64), Entries: 2, Bytes: 7}
		facts.Views = append(facts.Views, runtimescan.View{WorkloadName: a.WorkloadName, SourceTree: tree, ProjectionTree: tree})
		reports = append(reports, state.DeploymentRuntimeScanReport{WorkloadName: a.WorkloadName, Report: *composedWaveReport("sha256:"+tree.Digest, "sha256:"+hash)})
	}
	in, err := state.NewDeploymentRuntimeScanInput(uuid.NewString(), inputs, facts, reports)
	if err != nil {
		t.Fatal(err)
	}
	return in
}
