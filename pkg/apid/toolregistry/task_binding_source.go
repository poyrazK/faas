package toolregistry

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/tasktoolsource/sourcecheck"
)

type verifiedHostedTaskSource struct {
	intent  api.HostedTaskSourceIntent
	review  tasktoolsource.Review
	entries map[string][]api.HostedTaskSourceEntry
}

// loadHostedTaskSource opens and verifies the account-encrypted source for
// private consumers. The decoded entries never enter a public receipt.
func (e *PrivateExecutor) loadHostedTaskSource(ctx context.Context, account, id string) (verifiedHostedTaskSource, error) {
	var zero verifiedHostedTaskSource
	journal, ok := e.store.(state.HostedTaskSourceIntentStore)
	if !ok || e.opts.ConnectionIdentities == nil {
		return zero, state.ErrConflict
	}
	row, err := journal.GetHostedTaskSourceIntent(ctx, account, id)
	if err != nil {
		return zero, err
	}
	if row.Record.AccountID != account || row.Record.ID != id || row.Record.SourceState != "retained" ||
		row.Record.ReviewStatus != "approved" || len(row.Ciphertext) > api.HostedTaskSourceCiphertextMaxBytes {
		return zero, state.ErrConflict
	}
	ns, plain, err := secretbox.OpenBytesMulti(e.opts.ConnectionIdentities(ctx), row.Ciphertext)
	defer clear(plain)
	want := "gregale-hosted-task-source:" + account + ":" + row.Record.RequesterKeyID + ":" + id + ":" + row.Record.ReviewSHA256
	if err != nil || ns != want || len(plain) > api.HostedTaskSourceRequestMaxBytes {
		return zero, state.ErrConflict
	}
	var sources map[string][]api.HostedTaskSourceEntry
	if json.Unmarshal(plain, &sources) != nil {
		return zero, state.ErrConflict
	}
	review, err := sourcecheck.ValidatePrepare(account, api.HostedTaskSourcePrepare{Review: row.Record.Review, Sources: sources})
	if err != nil || review.Fingerprint != row.Record.ReviewSHA256 {
		return zero, state.ErrConflict
	}
	return verifiedHostedTaskSource{intent: row.Record, review: review, entries: sources}, nil
}

func (e *PrivateExecutor) hostedBindingPolicy(ctx context.Context, account, id string) (tasktoolbinding.Policy, error) {
	source, err := e.loadHostedTaskSource(ctx, account, id)
	if err != nil {
		return tasktoolbinding.Policy{}, err
	}
	return reviewedHostedBindingPolicy(source.entries)
}

func reviewedHostedBindingPolicy(sources map[string][]api.HostedTaskSourceEntry) (tasktoolbinding.Policy, error) {
	var p tasktoolbinding.Policy
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
