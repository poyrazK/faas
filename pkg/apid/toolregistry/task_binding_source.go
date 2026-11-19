package toolregistry

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolsource/sourcecheck"
)

// Read only the server-retained, account-encrypted source. No customer code is
// evaluated and no source or runtime secret is returned in a receipt.
func (e *PrivateExecutor) hostedBindingPolicy(ctx context.Context, account, id string) (tasktoolbinding.Policy, error) {
	var p tasktoolbinding.Policy
	journal, ok := e.store.(state.HostedTaskSourceIntentStore)
	if !ok || e.opts.ConnectionIdentities == nil {
		return p, state.ErrConflict
	}
	row, err := journal.GetHostedTaskSourceIntent(ctx, account, id)
	if err != nil {
		return p, err
	}
	if row.Record.SourceState != "retained" || row.Record.ReviewStatus != "approved" || len(row.Ciphertext) > api.HostedTaskSourceCiphertextMaxBytes {
		return p, state.ErrConflict
	}
	ns, plain, err := secretbox.OpenBytesMulti(e.opts.ConnectionIdentities(ctx), row.Ciphertext)
	want := "gregale-hosted-task-source:" + account + ":" + row.Record.RequesterKeyID + ":" + id + ":" + row.Record.ReviewSHA256
	if err != nil || ns != want || len(plain) > api.HostedTaskSourceRequestMaxBytes {
		return p, state.ErrConflict
	}
	var sources map[string][]api.HostedTaskSourceEntry
	if json.Unmarshal(plain, &sources) != nil {
		return p, state.ErrConflict
	}
	if _, err := sourcecheck.ValidatePrepare(account, api.HostedTaskSourcePrepare{Review: row.Record.Review, Sources: sources}); err != nil {
		return p, state.ErrConflict
	}
	web, err := sourcecheck.SourceConfig(sources["web"], false)
	if err != nil {
		return p, state.ErrConflict
	}
	worker, err := sourcecheck.SourceConfig(sources["worker"], true)
	if err != nil {
		return p, state.ErrConflict
	}
	w, k := web.Tasks, worker.Tasks
	ownerID := w.OwnerKeyID
	if ownerID == "" {
		ownerID = "default"
	}
	workerID := k.OwnerKeyID
	if workerID == "" {
		workerID = "default"
	}
	if ownerID != workerID || w.PayloadEncryptionVersion != k.PayloadEncryptionVersion || len(w.PreviousOwnerKeys) != len(k.PreviousOwnerKeys) {
		return p, state.ErrConflict
	}
	p = tasktoolbinding.Policy{WebDatabaseEnv: w.DatabaseURLEnv, WorkerDatabaseEnv: k.DatabaseURLEnv, WebOwnerEnv: w.OwnerKeyEnv, WorkerOwnerEnv: k.OwnerKeyEnv, WebNamespaceEnv: w.NamespaceEnv, WorkerNamespaceEnv: k.NamespaceEnv, WebPayloadEnv: w.EncryptionKeysEnv, WorkerPayloadEnv: k.EncryptionKeysEnv, OwnerKeyID: ownerID, PayloadVersion: w.PayloadEncryptionVersion}
	previous := map[string]string{}
	for _, v := range k.PreviousOwnerKeys {
		previous[v.ID] = v.Env
	}
	for _, v := range w.PreviousOwnerKeys {
		env, ok := previous[v.ID]
		if !ok {
			return p, state.ErrConflict
		}
		p.PreviousKeys = append(p.PreviousKeys, tasktoolbinding.PreviousKey{ID: v.ID, WebEnv: v.Env, WorkerEnv: env})
	}
	sort.Slice(p.PreviousKeys, func(i, j int) bool { return p.PreviousKeys[i].ID < p.PreviousKeys[j].ID })
	if !p.Valid() {
		return p, state.ErrConflict
	}
	return p, nil
}
