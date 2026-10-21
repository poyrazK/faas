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

	refs, baseline, reason := state.ObservedGitOpsSecretReferences(app, map[string]bool{"TOKEN": true})
	if reason != "" || refs["TOKEN"] != "secret:TOKEN" || baseline != `["legacy"]` {
		t.Fatalf("managed deployment changed inherited secret baseline: refs=%v baseline=%s reason=%q", refs, baseline, reason)
	}

	app.LiveDeployments[1].SecretRefs = json.RawMessage(`{"TOKEN":"secret:OTHER"}`)
	if _, _, reason := state.ObservedGitOpsSecretReferences(app, map[string]bool{"TOKEN": true}); reason == "" {
		t.Fatal("managed deployment with a conflicting secret reference was not rejected")
	}
}
