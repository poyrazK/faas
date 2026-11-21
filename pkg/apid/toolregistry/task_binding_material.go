package toolregistry

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (e *PrivateExecutor) probeHostedTaskBindings(ctx context.Context, account string, row state.HostedTaskBindingRecord) []api.HostedTaskBindingResult {
	results := tasktoolbinding.Results("not_checked")
	results[0].Status = "passed" // Namespace was checked by the native claim.
	values := map[string]string{}
	for _, pin := range row.Pins {
		value, err := e.hostedBindingSecret(ctx, account, pin)
		if err != nil {
			results[1].Status = "failed"
			return results
		}
		values[pin.Role] = value
	}
	keys := tasktoolbinding.OwnerKeysMatch(values["web_owner"], values["worker_owner"])
	for _, key := range row.Policy.PreviousKeys {
		keys = tasktoolbinding.OwnerKeysMatch(values["web_previous:"+key.ID], values["worker_previous:"+key.ID]) && keys
	}
	results[1].Status = "failed"
	if keys {
		results[1].Status = "passed"
	}
	results[2].Status = "passed"
	if row.Policy.WebPayloadEnv != "" && !tasktoolbinding.PayloadKeysMatch(values["web_payload"], values["worker_payload"]) {
		results[2].Status = "failed"
	}
	if !keys || results[2].Status != "passed" {
		return results
	}
	dsns := [4]string{values["web_database"], values["worker_database"], values["migration_database"], values["observer_database"]}
	checks, _ := tasktoolbinding.Probe(ctx, dsns, e.opts.BindingProbeConnect)
	copy(results[3:], checks)
	return results
}
func (e *PrivateExecutor) hostedBindingSecret(ctx context.Context, account string, pin tasktoolbinding.Pin) (string, error) {
	row, err := e.store.GetAppSecretInScope(ctx, account, pin.AppID, api.DefaultEnvScope, pin.Key)
	if err != nil {
		return "", err
	}
	if row == nil || row.SecretVersion != pin.SecretVersion || row.ManagedPostgresBindingID != pin.BindingID || row.ManagedCredentialRef != pin.CredentialRef || row.ManagedCredentialGeneration != pin.Generation || toolpublication.Digest(row.Ciphertext) != pin.CipherSHA256 {
		return "", state.ErrConflict
	}
	values, binding, err := secretbox.OpenMultiBound(e.opts.Identities(ctx), row.Ciphertext)
	if err != nil || binding.Require(account) != nil || len(values) != 1 || len(values[pin.Key]) == 0 || len(values[pin.Key]) > api.HostedTaskBindingSecretMaxBytes {
		return "", state.ErrConflict
	}
	return values[pin.Key], nil
}
