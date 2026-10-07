package imaged

// adr: 435. Exact stream and store checks; fixture ext4 is not native evidence.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/state"
)

func configureSourceBaseFixture(t *testing.T, f *sourceExportFixture) {
	t.Helper()
	base, _, _, _, ref := verifiedBaseFixture(t, "complete")
	base.store = f.store
	_, runtime := state.SourceBuildRootfsKind(f.app, f.dep)
	key := state.RuntimeBaseKeyForArch(runtime, "amd64")
	if _, err := base.EnsureBaseExt4(t.Context(), ref, key, key+".digest", "", "", ""); err != nil {
		t.Fatal(err)
	}
	f.h.WithStorage(base.storage).WithRuntimeBaseStaging()
	f.h.guestInitPath = base.guestInitPath
	f.h.oci = base.oci
	f.h.deployBaseRefOverride = ref
	f.builder.guestInitDigest = imagechain.Digest([]byte("ACTUAL INIT"))
	if runtime == RuntimeGo124 {
		f.h.WithFunctionRunnerGo124("/test/runner")
	}
}

func TestSourceRootfsConversionPublishesExactBindings(t *testing.T) {
	for _, function := range []bool{false, true} {
		t.Run(map[bool]string{false: "container", true: "function"}[function], func(t *testing.T) {
			f := newSourceExportFixture(t, state.DeploymentKindTarball, function, true)
			parent, err := f.h.approvedSourceExport(t.Context(), f.app, f.dep)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err != nil {
				t.Fatal(err)
			}
			value, err := f.store.GetCurrentSourceBuildRootfs(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			kind, runtime := state.SourceBuildRootfsKind(f.app, f.dep)
			base, err := f.store.GetCurrentBaseImageProducer(t.Context(), state.RuntimeBaseKeyForArch(runtime, "amd64"))
			if err != nil {
				t.Fatal(err)
			}
			if value.Input.PublicationID != parent.ID || value.Input.PublicationHash != parent.InputHash || value.Input.Kind != kind || value.Input.BaseProducerID != base.ID || value.Input.BaseInputHash != base.InputHash || value.Input.GuestInitDigest != base.Input.GuestInitDigest || value.Input.ArtifactDigest != imagechain.Digest([]byte("fake ext4")) || value.Input.ArtifactBytes != 9 {
				t.Fatal("conversion bindings lost")
			}
			if function && value.Input.RunnerDigest != testRunnerDigest || !function && value.Input.RunnerDigest != "" {
				t.Fatal("runner binding lost")
			}
			if inputs, err := f.store.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID); err != nil || len(inputs.Artifacts) != 2 || inputs.Artifacts[1].Kind != kind {
				t.Fatal("source conversion cannot bootstrap composed scan", err)
			}
		})
	}
}

func TestSourceRootfsLateChangesRefuseAtomicStamp(t *testing.T) {
	for _, mode := range []string{"command", "manifest", "signature policy", "base selection", "guest bytes", "stored output", "missing injected digest"} {
		t.Run(mode, func(t *testing.T) {
			f := newSourceExportFixture(t, state.DeploymentKindTarball, false, true)
			f.builder.buildHook = func() {
				switch mode {
				case "command":
					command := "/app/new-command"
					if _, err := f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{StartCommand: &command}); err != nil {
						t.Fatal(err)
					}
				case "manifest":
					manifest := f.app.Manifest
					manifest.Env = map[string]string{"CHANGED_STARTUP": "yes"}
					if _, err := f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
						t.Fatal(err)
					}
				case "signature policy":
					policy := api.AppSecurityPolicyEnforce
					if _, err := f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{SecurityPolicy: &policy, SetSecurityPolicy: true}); err != nil {
						t.Fatal(err)
					}
				case "base selection":
					base, err := f.store.GetCurrentBaseImageProducer(t.Context(), state.RuntimeBaseKeyForArch("", "amd64"))
					if err != nil {
						t.Fatal(err)
					}
					base.Input.ID = uuid.NewString()
					if _, err := f.store.PublishBaseImageProducer(t.Context(), base.Input); err != nil {
						t.Fatal(err)
					}
				case "guest bytes":
					if err := os.WriteFile(f.h.guestInitPath, []byte("changed init"), 0755); err != nil {
						t.Fatal(err)
					}
				case "stored output":
					in := f.builder.calls[0]
					if err := in.Storage.Put(t.Context(), in.StorageKey, bytes.NewReader([]byte("altered ext4"))); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "missing injected digest" {
				f.builder.guestInitDigest = ""
			}
			if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err == nil {
				t.Fatal("late change published")
			}
			dep, err := f.store.DeploymentByID(t.Context(), f.dep.ID)
			if err != nil || dep.RootfsPath != f.dep.RootfsPath || dep.RootfsBytes != f.dep.RootfsBytes {
				t.Fatal("refusal partially stamped deployment", err)
			}
			if _, err := f.store.GetCurrentSourceBuildRootfs(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("refusal left producer", err)
			}
		})
	}
}

func TestSourceRootfsBindingDoesNotSerializePrivateEvidence(t *testing.T) {
	f := newSourceExportFixture(t, state.DeploymentKindTarball, false, true)
	approval, err := f.h.approvedSourceExport(t.Context(), f.app, f.dep)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.h.prepareSourceBuildRootfsBinding(t.Context(), f.app, f.dep, *approval)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(binding)
	if err != nil || string(raw) != "{}" {
		t.Fatal("private conversion evidence serialized", err)
	}
}
