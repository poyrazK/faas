package builderd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SealedBuildSecret is one secret's ciphertext for a Dockerfile build
// (ADR-20261022185329). builderd never opens it: vmmd, the runtime domain's
// only reader, opens it into the builder VM's own drive. Key is the name the
// build reads; SourceKey names the sealed secret when that differs, for an
// alias or a project environment secret (ADR-20261024140000).
type SealedBuildSecret struct {
	Key        string
	SourceKey  string
	Ciphertext []byte
}

// errBuildSecretMissing marks a build whose declared secrets are not set or
// not available to it; it is the customer's to fix, not an infrastructure
// failure.
var errBuildSecretMissing = errors.New("build secrets are not set")

// loadBuildSecrets reads the ciphertext of the secrets a build mounts. Each
// name resolves to the secret the deployment's app receives as that
// environment variable at runtime (ADR-20261024140000): its own secret in the
// deployment's scope, an alias the environment declares, or a project
// environment secret it inherits. Values are never read here; a missing
// secret is reported by name.
func loadBuildSecrets(ctx context.Context, store state.Store, app state.App, dep state.Deployment, secrets []api.DockerfileBuildSecret) ([]SealedBuildSecret, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	scope := dep.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return nil, fmt.Errorf("load build secrets: %w", err)
	}
	var intent state.AppEnvironmentSecretIntent
	if reader, ok := store.(state.AppEnvironmentSecretIntentReader); ok {
		if intent, err = reader.AppEnvironmentSecretIntent(ctx, app.AccountID, app.ID, scope); err != nil {
			return nil, fmt.Errorf("load build secret references: %w", err)
		}
	}
	names := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		names = append(names, secret.Secret)
	}
	sources, missing, err := state.ResolveRuntimeSecretSources(snapshot, intent, names)
	if errors.Is(err, state.ErrRuntimeSecretReleaseOnly) {
		return nil, fmt.Errorf("%w: %w", errBuildSecretMissing, err)
	}
	if err != nil {
		return nil, fmt.Errorf("load build secrets: %w", err)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s not set in scope %s; set them with `gregale secrets set`", errBuildSecretMissing, strings.Join(missing, ", "), scope)
	}
	sealed := make([]SealedBuildSecret, len(sources))
	for i, source := range sources {
		sealed[i] = SealedBuildSecret{Key: source.Key, SourceKey: source.SourceKey, Ciphertext: source.Ciphertext}
	}
	return sealed, nil
}

// buildSecretsNeedAliasSupport reports whether vmmd must open an envelope
// whose key differs from the name the build reads (ADR-423).
func buildSecretsNeedAliasSupport(secrets []SealedBuildSecret) bool {
	for _, secret := range secrets {
		if secret.SourceKey != "" {
			return true
		}
	}
	return false
}

// sealedBuildSecretsToProto hands the ciphertext to vmmd in the builder's
// AppSpec.sealed_env, the field an app VM's secrets use.
func sealedBuildSecretsToProto(secrets []SealedBuildSecret) []*vmmdpb.SealedSecret {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]*vmmdpb.SealedSecret, len(secrets))
	for i, secret := range secrets {
		out[i] = &vmmdpb.SealedSecret{Key: secret.Key, Ciphertext: secret.Ciphertext, SourceKey: secret.SourceKey}
	}
	return out
}
