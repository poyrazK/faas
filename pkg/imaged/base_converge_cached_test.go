// adr: 632
package imaged

import (
	"context"
	"testing"
)

// mapEnv is an envLookup over a fixed map.
func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestConvergeBasesTracksABaseCachedByAnEarlierDaemon pins the production-us
// rc.243 divergence: this daemon never staged node22 (no deployment of it
// landed here since start), yet the copy an earlier daemon cached must still
// converge to the shared publication.
func TestConvergeBasesTracksABaseCachedByAnEarlierDaemon(t *testing.T) {
	f := newConvergeFixture(t)
	f.publishContent(t, "other-node-build")
	f.h.convergeBases(context.Background(), "amd64", mapEnv(map[string]string{"FAAS_DEPLOY_BASE_REF_NODE22": convergeRef}))
	if got := f.local(t); got != "other-node-build" {
		t.Fatalf("local base = %q, want the published bytes", got)
	}
	if got := f.h.baseConvergence.staged[convergeBaseKey]; got != f.staged {
		t.Fatalf("staged = %+v, want %+v", got, f.staged)
	}
	if _, ok := f.h.baseConvergence.staged["base/runner-python312-amd64.ext4"]; ok {
		t.Fatal("tracked a base that is not cached on this node")
	}
}

func TestConvergeBasesLeavesACachedBaseOfAnotherRecipe(t *testing.T) {
	cases := map[string]map[string]string{
		// The node would build node22 from a different ref than the
		// publication's, so the publication is not its recipe.
		"other source ref": {"FAAS_DEPLOY_BASE_REF_NODE22": otherRef},
		// A named node refuses the unpinned default ref; the cached copy's
		// recipe is unknown, so it is not guessed.
		"unresolvable ref": {"FAAS_NODE_NAME": "compute-2"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			f := newConvergeFixture(t)
			f.publishContent(t, "other-node-build")
			f.h.convergeBases(context.Background(), "amd64", mapEnv(env))
			if got := f.local(t); got != "own-build" {
				t.Fatalf("local base = %q, want it untouched", got)
			}
		})
	}
}

func TestRememberCachedBasesKeepsTheStagedRecipe(t *testing.T) {
	f := newConvergeFixture(t)
	f.h.rememberStagedBase(otherRef, convergeBaseKey, convergeDigestKey)
	f.h.rememberCachedBases(context.Background(), f.cache, "amd64", mapEnv(map[string]string{"FAAS_DEPLOY_BASE_REF_NODE22": convergeRef}))
	if got := f.h.baseConvergence.staged[convergeBaseKey].ref; got != otherRef {
		t.Fatalf("staged ref = %q, want the ref this daemon staged", got)
	}
}
