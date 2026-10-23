package builderd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SealedBuildSecret is one app secret's ciphertext for a Dockerfile build
// (ADR-20261022185329). builderd never opens it: vmmd, the runtime domain's
// only reader, opens it into the builder VM's own drive.
type SealedBuildSecret struct {
	Key        string
	Ciphertext []byte
}

// errBuildSecretMissing marks a build whose declared secrets are not set; it
// is the customer's to fix, not an infrastructure failure.
var errBuildSecretMissing = errors.New("build secrets are not set")

// loadBuildSecrets reads the ciphertext of the app secrets a build mounts, in
// the deployment's scope, as schedd does for a wake. Values are never read
// here; a missing secret is reported by name.
func loadBuildSecrets(ctx context.Context, store state.Store, app state.App, dep state.Deployment, secrets []api.DockerfileBuildSecret) ([]SealedBuildSecret, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	scope := dep.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	rows, err := store.ListAppSecretsInScope(ctx, app.AccountID, app.ID, scope)
	if err != nil {
		return nil, fmt.Errorf("load build secrets: %w", err)
	}
	byKey := make(map[string]state.AppSecret, len(rows))
	for _, row := range rows {
		byKey[row.Key] = row
	}
	var sealed []SealedBuildSecret
	var missing []string
	for _, secret := range secrets {
		if slices.ContainsFunc(sealed, func(s SealedBuildSecret) bool { return s.Key == secret.Secret }) || slices.Contains(missing, secret.Secret) {
			continue
		}
		row, ok := byKey[secret.Secret]
		if !ok || len(row.Ciphertext) == 0 {
			missing = append(missing, secret.Secret)
			continue
		}
		sealed = append(sealed, SealedBuildSecret{Key: row.Key, Ciphertext: append([]byte(nil), row.Ciphertext...)})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s not set in scope %s; set them with `gregale secrets set`", errBuildSecretMissing, strings.Join(missing, ", "), scope)
	}
	return sealed, nil
}

// sealedBuildSecretsToProto hands the ciphertext to vmmd in the builder's
// AppSpec.sealed_env, the field an app VM's secrets use.
func sealedBuildSecretsToProto(secrets []SealedBuildSecret) []*vmmdpb.SealedSecret {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]*vmmdpb.SealedSecret, len(secrets))
	for i, secret := range secrets {
		out[i] = &vmmdpb.SealedSecret{Key: secret.Key, Ciphertext: secret.Ciphertext}
	}
	return out
}
