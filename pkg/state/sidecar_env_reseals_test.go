package state_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestSidecarEnvReseals pins the copies the runtime re-seal writes beside a
// deployment's sidecar env (ADR 20261022111812): a copy is written only
// over the copy it was read with, and the operator listing finds the
// deployments that declare env values.
func TestSidecarEnvReseals(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	kid := id.Recipient().String()
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			declared := []byte("declared ciphertext")
			raw, err := json.Marshal([]map[string]any{
				{"name": "proxy", "image": "ghcr.io/org/proxy@sha256:01", "type": "sidecar", "env": map[string]string{"TOKEN": base64.StdEncoding.EncodeToString(declared)}},
				{"name": "plain", "image": "ghcr.io/org/plain@sha256:02", "type": "sidecar"},
			})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := f.store.CreateDeployment(ctx, state.Deployment{
				AppID: f.appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:sidecar-env", Status: state.DeployPending, Sidecars: raw,
			})
			if err != nil {
				t.Fatal(err)
			}
			list := func() []state.SidecarEnvReseal {
				t.Helper()
				out, err := f.store.ListDeploymentSidecarEnvReseals(ctx, dep.ID)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			if got := list(); len(got) != 0 {
				t.Fatalf("copies before the re-seal = %+v, want none", got)
			}
			upsert := f.store.(interface {
				UpsertSidecarEnvReseal(context.Context, state.SidecarEnvReseal, []byte) (bool, error)
			}).UpsertSidecarEnvReseal
			first := state.SidecarEnvReseal{DeploymentID: dep.ID, SidecarName: "proxy", Key: "TOKEN",
				SourceSHA256: state.SidecarEnvSourceDigest(declared), Kid: kid, Ciphertext: []byte("first")}
			second := first
			second.Ciphertext = []byte("second")
			other := first
			other.SidecarName = "plain"
			for _, step := range []struct {
				name     string
				r        state.SidecarEnvReseal
				previous []byte
				want     bool
			}{
				{"first copy", first, nil, true},
				{"a second first copy", second, nil, false},
				{"over a copy it was not read with", second, []byte("other"), false},
				{"over the copy it was read with", second, []byte("first"), true},
				// The copy it was read with is gone: write the new one.
				{"over a copy since removed", other, []byte("gone"), true},
			} {
				if ok, err := upsert(ctx, step.r, step.previous); err != nil || ok != step.want {
					t.Fatalf("%s = %v, %v; want %v", step.name, ok, err, step.want)
				}
			}
			if got := list(); len(got) != 2 || got[0].SidecarName != "plain" || string(got[1].Ciphertext) != "second" || got[1].SourceSHA256 != first.SourceSHA256 || got[1].Kid != kid {
				t.Fatalf("copies = %+v, want plain's and the second", got)
			}
			invalid := second
			invalid.Kid = ""
			if _, err := upsert(ctx, invalid, []byte("second")); err == nil {
				t.Fatal("a copy without a key id was accepted")
			}

			lister, ok := f.store.(interface {
				ListSidecarEnvDeployments(context.Context, string, int) ([]state.DeploymentSidecars, error)
			})
			if !ok {
				return // the operator listing is on PgStore alone
			}
			page, err := lister.ListSidecarEnvDeployments(ctx, "", 1000)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range page {
				found = found || d.ID == dep.ID
				if d.ID == f.liveDep {
					t.Fatalf("listed deployment %s, which declares no sidecar env", d.ID)
				}
			}
			if !found {
				t.Fatalf("listing %+v misses the deployment with sidecar env", page)
			}
		})
	}
}
