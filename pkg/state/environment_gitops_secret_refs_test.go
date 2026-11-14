package state_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestObservedGitOpsSecretReferencesPinsOnlyUnmanagedDeploymentIdentity(t *testing.T) {
	app := state.GitOpsIntentApp{
		SecretNames: []string{"TOKEN"},
		LiveDeployments: []state.GitOpsSecretBaseline{
			{ID: "legacy", SecretRefs: json.RawMessage(`{"TOKEN":"secret:TOKEN"}`)},
			{ID: "candidate", SecretRefs: json.RawMessage(`{"TOKEN":"secret:TOKEN"}`), Managed: true},
		},
	}

	refs, baseline, reason := state.ObservedGitOpsSecretReferences(app, map[string]bool{"TOKEN": true}, nil)
	if reason != "" || refs["TOKEN"] != "secret:TOKEN" || baseline != `["legacy"]` {
		t.Fatalf("managed deployment changed inherited secret baseline: refs=%v baseline=%s reason=%q", refs, baseline, reason)
	}

	app.LiveDeployments[1].SecretRefs = json.RawMessage(`{"TOKEN":"secret:OTHER"}`)
	if _, _, reason := state.ObservedGitOpsSecretReferences(app, map[string]bool{"TOKEN": true}, nil); reason == "" {
		t.Fatal("managed deployment with a conflicting secret reference was not rejected")
	}
}

func TestObservedGitOpsSecretReferencesAllowMissingSourceOnlyWhenPruned(t *testing.T) {
	app := state.GitOpsIntentApp{SecretRefs: map[string]string{"URL": "secret:REMOVED"}}
	for _, pruned := range []bool{false, true} {
		refs, _, reason := state.ObservedGitOpsSecretReferences(app, map[string]bool{"URL": true}, map[string]bool{"URL": pruned})
		if (reason == "") != pruned {
			t.Fatalf("pruned=%t, validation=%q", pruned, reason)
		}
		if pruned && refs["URL"] != "secret:REMOVED" {
			t.Fatal("pruning lost the observed reference needed for the removal plan")
		}
	}
}
