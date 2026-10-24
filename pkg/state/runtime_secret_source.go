package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrRuntimeSecretReleaseOnly marks a key whose secret only release tasks
// receive, such as a managed PostgreSQL migration credential.
var ErrRuntimeSecretReleaseOnly = errors.New("secret is restricted to release tasks")

// RuntimeSecretSource is the sealed secret one environment key receives in a
// deployment's main workload. SourceKey names the single key inside the sealed
// envelope when the environment key reads another secret (an alias) or a
// project environment secret (ADR-423); empty means the envelope's own key.
type RuntimeSecretSource struct {
	Key        string
	SourceKey  string
	Ciphertext []byte
}

// ResolveRuntimeSecretSources resolves environment keys to the sealed secrets
// a deployment's serving process receives for them, as schedd's wake does:
// the deployment's explicit secret grants, or every scoped app secret when it
// has none, overlaid by the environment's references and suppressions; then,
// only without explicit grants, the project environment's secrets for keys the
// app does not set (ADR-1139). Release-task credentials are refused, as for
// any serving process. Keys with no source are returned in missing, in order.
// Builds use it so a build secret reads what the app reads at runtime
// (ADR-20261024140000).
func ResolveRuntimeSecretSources(snapshot RuntimeAppValuesSnapshot, intent AppEnvironmentSecretIntent, keys []string) ([]RuntimeSecretSource, []string, error) {
	refs, err := DeploymentSecretReferences(snapshot.SecretGrants.OverrideEnvSecrets)
	if err != nil {
		return nil, nil, fmt.Errorf("decode deployment secret grants: %w", err)
	}
	explicit := len(refs) > 0
	index := make(map[string]AppSecret, len(snapshot.Secrets))
	for _, row := range snapshot.Secrets {
		index[row.Key] = row
	}
	if !explicit {
		refs = make(map[string]string, len(snapshot.Secrets))
		for _, row := range snapshot.Secrets {
			refs[row.Key] = api.SecretRefPrefix + row.Key
		}
	}
	refs = intent.EffectiveReferences(refs)
	inherited := make(map[string]ProjectEnvironmentSecret, len(snapshot.InheritedSecrets))
	for _, secret := range snapshot.InheritedSecrets {
		inherited[secret.Key] = secret
	}

	var sources []RuntimeSecretSource
	var missing []string
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		if ref, ok := refs[key]; ok {
			if !ValidSecretReference(ref) {
				return nil, nil, fmt.Errorf("invalid secret reference for environment key %q", key)
			}
			source := strings.TrimPrefix(ref, api.SecretRefPrefix)
			row, ok := index[source]
			if !ok || len(row.Ciphertext) == 0 {
				missing = append(missing, key)
				continue
			}
			if _, err := SelectAppSecretsForDelivery([]AppSecret{row}, map[string]string{source: ref}, false); err != nil {
				return nil, nil, fmt.Errorf("%w: environment key %q reads %q: %w", ErrRuntimeSecretReleaseOnly, key, source, err)
			}
			resolved := RuntimeSecretSource{Key: key, Ciphertext: append([]byte(nil), row.Ciphertext...)}
			if _, referenced := intent.References[key]; referenced || key != row.Key {
				resolved.SourceKey = row.Key
			}
			sources = append(sources, resolved)
			continue
		}
		if secret, ok := inherited[key]; ok && !explicit && len(secret.Ciphertext) > 0 {
			sources = append(sources, RuntimeSecretSource{Key: key, SourceKey: secret.Key, Ciphertext: append([]byte(nil), secret.Ciphertext...)})
			continue
		}
		missing = append(missing, key)
	}
	return sources, missing, nil
}
