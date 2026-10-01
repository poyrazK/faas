package state

import (
	"encoding/json"
	"maps"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type gitOpsSecretBaseline struct {
	ID         string          `json:"id"`
	SecretRefs json.RawMessage `json:"secret_refs"`
}

func gitOpsSecretBaselineResource(resource string) string {
	return "secret_reference_baseline/" + resource
}

// All live deployments must agree after scoped intent is applied. Adoption
// cannot choose one canary mapping and silently discard another serving value.
func observedGitOpsSecretReferences(app gitOpsIntentApp) (map[string]string, string, string) {
	intent := AppEnvironmentSecretIntent{References: app.SecretRefs, SuppressedKeys: app.SuppressedKeys}
	current := intent.EffectiveReferences(nil)
	ids := make([]string, 0, len(app.LiveDeployments))
	var observed map[string]string
	for _, deployment := range app.LiveDeployments {
		ids = append(ids, deployment.ID)
		refs, err := DeploymentSecretReferences(deployment.SecretRefs)
		if err != nil {
			return nil, "", "persisted deployment secret references are invalid"
		}
		if len(refs) == 0 {
			refs = map[string]string{}
			for _, name := range app.SecretNames {
				refs[name] = api.SecretRefPrefix + name
			}
		}
		refs = intent.EffectiveReferences(refs)
		if observed != nil && !maps.Equal(observed, refs) {
			return nil, "", "live deployments disagree on secret references; finish or abort the rollout before adoption"
		}
		observed = refs
	}
	if observed != nil {
		current = observed
	}
	names := map[string]bool{}
	for _, name := range app.SecretNames {
		names[name] = true
	}
	for key, ref := range current {
		if api.ValidateEnvKey(key) != nil || !ValidSecretReference(ref) {
			return nil, "", "scoped secret references are invalid"
		}
		if !names[strings.TrimPrefix(ref, api.SecretRefPrefix)] {
			return nil, "", "a current secret reference source is unavailable in this environment"
		}
	}
	sort.Strings(ids)
	raw, _ := json.Marshal(ids)
	return current, string(raw), ""
}

func validateGitOpsSecretRefs(out *EnvironmentGitOpsObservation, desired environmentsync.DesiredState, resource string, app gitOpsIntentApp) {
	names := map[string]bool{}
	for _, name := range app.SecretNames {
		names[name] = true
	}
	workload := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")]
	for key, ref := range workload.SecretRefs {
		if !names[strings.TrimPrefix(ref, api.SecretRefPrefix)] {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#secret_refs/"+key+": referenced secret is unavailable in this environment")
		}
		if _, exists := app.Variables[key]; exists {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#secret_refs/"+key+": remove the non-secret variable before adopting this reference")
		}
	}
}
