package state

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type gitOpsSecretBaseline struct {
	ID         string          `json:"id"`
	SecretRefs json.RawMessage `json:"secret_refs"`
	Managed    bool            `json:"managed,omitempty"`
}

func gitOpsSecretBaselineResource(resource string) string {
	return "secret_reference_baseline/" + resource
}

// Live deployments must agree on desired or owned keys after scoped intent is applied. Adoption
// cannot choose one canary mapping and silently discard another serving value.
func observedGitOpsSecretReferences(app gitOpsIntentApp, relevant map[string]bool) (map[string]string, string, string) {
	intent := AppEnvironmentSecretIntent{References: app.SecretRefs, SuppressedKeys: app.SuppressedKeys}
	current := intent.EffectiveReferences(nil)
	ids := make([]string, 0, len(app.LiveDeployments))
	var observed map[string]string
	for _, deployment := range app.LiveDeployments {
		if !deployment.Managed {
			ids = append(ids, deployment.ID)
		}
		if len(relevant) == 0 {
			continue
		}
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
		for key := range refs {
			if !relevant[key] {
				delete(refs, key)
			}
		}
		if observed != nil {
			for key := range relevant {
				if observed[key] != refs[key] {
					return nil, "", "live deployments disagree on managed secret references; finish or abort the rollout before adoption"
				}
			}
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
		if !relevant[key] {
			continue
		}
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
