package sched

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStandardNativeSourcesBindCapturedProducerBytes(t *testing.T) {
	now := time.Now()
	b := runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ProtocolVersion, Token: uuid.NewString(), InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), AccountID: uuid.NewString(), NodeID: uuid.NewString(), Incarnation: uuid.NewString(), DesiredRevision: 1, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64), EgressRevision: 1, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	app := AppSpec{AppID: b.AppID, AccountID: b.AccountID, DeploymentID: b.DeploymentID, Plan: api.PlanPro, BaseKey: "base/a.ext4", LayerKey: "rootfs/main.ext4", VCPUCount: 2, MemSizeMiB: 128}
	digest := "sha256:" + strings.Repeat("c", 64)
	capture := state.InstanceApplicationStandardAdmission{RuntimeArtifacts: []state.DeploymentRuntimeArtifact{{Kind: "base-image", StorageKey: app.BaseKey, Digest: digest, Bytes: 10}, {Kind: "app-layer", StorageKey: app.LayerKey, Digest: digest, Bytes: 20}}}
	req, err := prepareStandardAdmittedRuntime(t.Context(), b, capture, app, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.ArtifactSources) != 2 || req.ArtifactSources[0].StorageKey != app.BaseKey || req.ArtifactSources[1].Bytes != 20 {
		t.Fatal("captured set was not delivered")
	}
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil || hash != req.Binding.PayloadHash {
		t.Fatal("source set omitted from reviewed boot payload")
	}
	capture.RuntimeArtifacts[1].Digest = "sha256:" + strings.Repeat("d", 64)
	if req.ArtifactSources[1].Digest != digest {
		t.Fatal("native request aliases captured producers")
	}
	req.ArtifactSources[1].Bytes++
	changed, _ := runtimeadmission.HashBootPayload(req)
	if changed == hash {
		t.Fatal("source size change retained grant authority")
	}
	capture.RuntimeArtifacts = capture.RuntimeArtifacts[1:]
	if _, err := prepareStandardAdmittedRuntime(t.Context(), b, capture, app, nil, false); err == nil {
		t.Fatal("unbound default base silently exempted")
	}
}
