package promotion

import (
	"encoding/base64"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func TokenWithSecrets(token string, refs map[string]map[string]string) (string, error) {
	if err := api.ValidatePromotionSecretReferences(refs); err != nil {
		return "", err
	}
	wire, err := DecodeToken(token)
	if err != nil {
		return "", err
	}
	wire.ReleaseSecrets = refs
	raw, err := json.Marshal(wire)
	return base64.RawURLEncoding.EncodeToString(raw), err
}

func SecretChanges(config *api.ProjectReleaseSecretConfiguration, workload string) []api.ProjectEnvironmentConfigChange {
	if config == nil {
		return nil
	}
	before, after := config.PreviousVersions[workload], config.Versions[workload]
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	var changes []api.ProjectEnvironmentConfigChange
	for key := range keys {
		old, had := before[key]
		next, has := after[key]
		kind := "changed"
		if !had {
			kind = "added"
		} else if !has {
			kind = "removed"
		} else if old == next {
			continue
		}
		changes = append(changes, api.ProjectEnvironmentConfigChange{Key: key, Kind: kind})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
	return changes
}
