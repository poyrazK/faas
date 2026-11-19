package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// HostedTaskBindingRecord is private journal data; handlers return Public only.
type HostedTaskBindingRecord = tasktoolbinding.Record
type HostedTaskBindingStore interface {
	ChangeHostedTaskBindings(context.Context, string, string, string, HostedTaskBindingChange) (HostedTaskBindingRecord, error)
}

// Owner and Policy are server-populated, never accepted from request JSON.
type HostedTaskBindingChange struct {
	Owner                HostedTaskSourceOwner
	Selection            api.HostedTaskBindingSelection
	Policy               tasktoolbinding.Policy
	BindingSHA256, Claim string
	Checks               []api.HostedTaskBindingResult
}

var errHostedTaskBindingDrift = errors.New("native hosted Task binding drift")

type hostedTaskSecretSpec struct{ Role, AppID, Key, Kind string }

func hostedTaskSecretSpecs(source api.HostedTaskSourceIntent, in api.HostedTaskBindingSelection, p tasktoolbinding.Policy) ([]hostedTaskSecretSpec, error) {
	r, err := tasktoolsource.Decode(source.Review)
	if err != nil || !p.Valid() || tasktoolbinding.ValidateSelection(in) != nil {
		return nil, ErrInvalidArgument
	}
	apps := map[string]string{}
	for _, a := range r.Apps {
		if a.Role == "web" || a.Role == "worker" || a.Role == "observer" {
			if apps[a.Role] != "" {
				return nil, ErrInvalidArgument
			}
			apps[a.Role] = a.Review.AppID
		}
	}
	if apps["web"] == "" || apps["worker"] == "" || apps["observer"] == "" || in.WebDatabaseRef != api.SecretRefPrefix+p.WebDatabaseEnv || in.WorkerDatabaseRef != api.SecretRefPrefix+p.WorkerDatabaseEnv {
		return nil, ErrInvalidArgument
	}
	specs := []hostedTaskSecretSpec{{"web_database", apps["web"], p.WebDatabaseEnv, "runtime"}, {"worker_database", apps["worker"], p.WorkerDatabaseEnv, "runtime"}, {"migration_database", apps["worker"], strings.TrimPrefix(in.MigrationDatabaseRef, api.SecretRefPrefix), "migration"}, {"observer_database", apps["observer"], strings.TrimPrefix(in.ObserverDatabaseRef, api.SecretRefPrefix), "observer"}, {"web_owner", apps["web"], p.WebOwnerEnv, "key"}, {"worker_owner", apps["worker"], p.WorkerOwnerEnv, "key"}}
	if p.WebPayloadEnv != "" {
		specs = append(specs, hostedTaskSecretSpec{"web_payload", apps["web"], p.WebPayloadEnv, "key"}, hostedTaskSecretSpec{"worker_payload", apps["worker"], p.WorkerPayloadEnv, "key"})
	}
	for _, k := range p.PreviousKeys {
		specs = append(specs, hostedTaskSecretSpec{"web_previous:" + k.ID, apps["web"], k.WebEnv, "key"}, hostedTaskSecretSpec{"worker_previous:" + k.ID, apps["worker"], k.WorkerEnv, "key"})
	}
	sort.Slice(specs, func(i, j int) bool {
		a, b := specs[i], specs[j]
		if a.AppID != b.AppID {
			return a.AppID < b.AppID
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Role < b.Role
	})
	return specs, nil
}
func hostedTaskSecretPin(account string, s AppSecret, spec hostedTaskSecretSpec) (tasktoolbinding.Pin, error) {
	var p tasktoolbinding.Pin
	if s.AccountID != account || s.AppID != spec.AppID || s.Scope != api.DefaultEnvScope || s.Key != spec.Key || len(s.Ciphertext) == 0 || len(s.Ciphertext) > 2*api.HostedTaskBindingSecretMaxBytes || s.SecretVersion < 0 || s.ManagedObjectStorageCredentialID != "" {
		return p, errHostedTaskBindingDrift
	}
	if spec.Kind == "key" {
		if s.ManagedPostgresBindingID != "" {
			return p, errHostedTaskBindingDrift
		}
	} else {
		if uuid.Validate(s.ManagedPostgresBindingID) != nil || uuid.Validate(s.ManagedPostgresDatabaseID) != nil || s.ManagedCredentialRef == "" || s.ManagedCredentialGeneration < 1 {
			return p, errHostedTaskBindingDrift
		}
		valid := spec.Kind == "runtime" && s.ManagedPostgresAccess == "read_write" || spec.Kind == "migration" && s.ManagedPostgresAccess == "migration" || spec.Kind == "observer" && s.ManagedPostgresAccess == "read_only"
		if !valid {
			return p, errHostedTaskBindingDrift
		}
	}
	return tasktoolbinding.Pin{Role: spec.Role, AppID: s.AppID, Key: s.Key, SecretVersion: s.SecretVersion, CipherSHA256: toolpublication.Digest(s.Ciphertext), BindingID: s.ManagedPostgresBindingID, DatabaseID: s.ManagedPostgresDatabaseID, CredentialRef: s.ManagedCredentialRef, Generation: s.ManagedCredentialGeneration, Access: s.ManagedPostgresAccess}, nil
}
func hostedTaskPinsSameDatabase(pins []tasktoolbinding.Pin) bool {
	db := ""
	n := 0
	for _, p := range pins {
		if p.BindingID != "" {
			if db != "" && db != p.DatabaseID {
				return false
			}
			db = p.DatabaseID
			n++
		}
	}
	return n == 4 && db != ""
}
func cloneHostedTaskBinding(r HostedTaskBindingRecord) HostedTaskBindingRecord {
	b, _ := json.Marshal(r)
	var out HostedTaskBindingRecord
	_ = json.Unmarshal(b, &out)
	return out
}
func hostedTaskBindingError(err error) error {
	if errors.Is(err, tasktoolbinding.ErrInvalid) {
		return ErrInvalidArgument
	}
	if errors.Is(err, tasktoolbinding.ErrConflict) {
		return ErrConflict
	}
	return err
}
func refreshHostedTaskBinding(r HostedTaskBindingRecord, source api.HostedTaskSourceIntent, fresh bool, now time.Time) HostedTaskBindingRecord {
	r.Public = tasktoolbinding.View(r.Public, now)
	if source.ReviewStatus == "rejected" || source.ReviewStatus == "revoked" {
		r.Public.Status = "revoked"
		return r
	}
	if !fresh && (r.Public.Status == "selected" || r.Public.Status == "verifying" || r.Public.Status == "passed") {
		r.Public.Status = "stale"
	}
	return r
}
func applyHostedTaskBinding(r HostedTaskBindingRecord, action string, in HostedTaskBindingChange, now time.Time) (HostedTaskBindingRecord, error) {
	if action == "select" {
		if !reflect.DeepEqual(r.Selection, in.Selection) {
			return r, ErrConflict
		}
		return r, nil
	}
	out, err := tasktoolbinding.Mutate(r, action, in.BindingSHA256, in.Claim, in.Checks, now)
	return out, hostedTaskBindingError(err)
}

func (m *MemStore) ChangeHostedTaskBindings(ctx context.Context, account, id, action string, in HostedTaskBindingChange) (HostedTaskBindingRecord, error) {
	var zero HostedTaskBindingRecord
	if uuid.Validate(account) != nil || !toolpublication.ValidID(id) {
		return zero, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return m.changeHostedTaskBindingsLocked(ctx, account, id, action, in)
}

func (m *MemStore) changeHostedTaskBindingsLocked(ctx context.Context, account, id, action string, in HostedTaskBindingChange) (HostedTaskBindingRecord, error) {
	var zero HostedTaskBindingRecord
	if action != "get" && !m.hostedTaskSourceOwnerFreshLocked(account, in.Owner) {
		return zero, ErrNotFound
	}
	m.retireHostedSourceLocked(account)
	source, ok := m.hostedTaskSourceIntents[account+":"+id]
	if !ok {
		return zero, ErrNotFound
	}
	source.Record = tasktoolsource.View(source.Record, len(source.Ciphertext) > 0, time.Now())
	if tasktoolsource.ValidateRecord(source.Record, account, id) != nil {
		return zero, ErrInvalidArgument
	}
	r, exists := m.hostedTaskBindings[account+":"+id]
	if !exists && action != "select" {
		return zero, ErrNotFound
	}
	if exists && (tasktoolbinding.ValidateRecord(r) != nil || !hostedTaskBindingMatchesSource(r, source.Record)) {
		return zero, ErrInvalidArgument
	}
	if action == "revoke" {
		return m.saveHostedTaskBindingLocked(account, id, r, action, in)
	}
	fresh := source.Record.ReviewStatus == "approved" && source.Record.SourceState == "retained"
	if fresh {
		review, err := tasktoolsource.Decode(source.Record.Review)
		if err != nil {
			return zero, ErrInvalidArgument
		}
		fresh = m.hostedTaskSourceRequesterFreshLocked(account, source.Record.RequesterKeyID) && m.taskToolReleaseFreshLocked(account, source.Record.RequesterKeyID, tasktoolsource.Intent(review), false)
	}
	selection, policy := in.Selection, in.Policy
	if exists {
		selection, policy = r.Selection, r.Policy
		if r.OwnerKeyID != "" && !m.hostedTaskSourceOwnerFreshLocked(account, HostedTaskSourceOwner{KeyID: r.OwnerKeyID}) {
			fresh = false
		}
	}
	var pins []tasktoolbinding.Pin
	if fresh {
		var err error
		pins, err = m.captureHostedTaskPinsLocked(account, source.Record, selection, policy)
		if err != nil && !errors.Is(err, errHostedTaskBindingDrift) {
			return zero, err
		}
		fresh = err == nil
	}
	if !exists {
		if !fresh {
			return zero, ErrConflict
		}
		var err error
		r, err = tasktoolbinding.New(source.Record, selection, policy, pins, in.Owner.KeyID, time.Now())
		if err != nil {
			return zero, hostedTaskBindingError(err)
		}
	} else {
		fresh = fresh && reflect.DeepEqual(pins, r.Pins)
		r = refreshHostedTaskBinding(r, source.Record, fresh, time.Now())
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if action != "get" && !m.hostedTaskSourceOwnerFreshLocked(account, in.Owner) {
		return zero, ErrNotFound
	}
	// Authority can expire while pins are hashed under the mutex.
	if !m.hostedTaskSourceRequesterFreshLocked(account, source.Record.RequesterKeyID) || r.OwnerKeyID != "" && !m.hostedTaskSourceOwnerFreshLocked(account, HostedTaskSourceOwner{KeyID: r.OwnerKeyID}) {
		r = refreshHostedTaskBinding(r, source.Record, false, time.Now())
	}
	return m.saveHostedTaskBindingLocked(account, id, r, action, in)
}
func (m *MemStore) saveHostedTaskBindingLocked(account, id string, r HostedTaskBindingRecord, action string, in HostedTaskBindingChange) (HostedTaskBindingRecord, error) {
	out, err := applyHostedTaskBinding(r, action, in, time.Now())
	if err != nil {
		return HostedTaskBindingRecord{}, err
	}
	body, err := json.Marshal(out)
	if err != nil || len(body) > api.HostedTaskBindingRecordMaxBytes {
		return HostedTaskBindingRecord{}, ErrInvalidArgument
	}
	if m.hostedTaskBindings == nil {
		m.hostedTaskBindings = map[string]HostedTaskBindingRecord{}
	}
	m.hostedTaskBindings[account+":"+id] = cloneHostedTaskBinding(out)
	return cloneHostedTaskBinding(out), nil
}
func (m *MemStore) captureHostedTaskPinsLocked(account string, source api.HostedTaskSourceIntent, in api.HostedTaskBindingSelection, p tasktoolbinding.Policy) ([]tasktoolbinding.Pin, error) {
	specs, err := hostedTaskSecretSpecs(source, in, p)
	if err != nil {
		return nil, err
	}
	var pins []tasktoolbinding.Pin
	for _, s := range specs {
		secret, ok := m.secrets[secretKey{AppID: s.AppID, Scope: api.DefaultEnvScope, Key: s.Key}]
		if !ok {
			return nil, errHostedTaskBindingDrift
		}
		pin, err := hostedTaskSecretPin(account, secret, s)
		if err != nil {
			return nil, err
		}
		pins = append(pins, pin)
	}
	if !hostedTaskPinsSameDatabase(pins) {
		return nil, errHostedTaskBindingDrift
	}
	roles := map[string]string{}
	for _, s := range specs {
		roles[s.Role] = s.AppID
	}
	for _, n := range hostedTaskNamespaceSpecs(roles, p) {
		app, ok := m.apps[n.app]
		if !ok {
			return nil, errHostedTaskBindingDrift
		}
		v, has := app.Manifest.Env[n.key]
		e, ehas := m.envs[envKey{AppID: n.app, Scope: api.DefaultEnvScope, Key: n.key}]
		_, secret := m.secrets[secretKey{AppID: n.app, Scope: api.DefaultEnvScope, Key: n.key}]
		if !hostedTaskNamespaceMatches(v, has, e.Value, ehas, secret, in.Namespace) {
			return nil, errHostedTaskBindingDrift
		}
	}
	return pins, nil
}

func hostedTaskNamespaceMatches(manifest string, manifestHas bool, env string, envHas, secret bool, namespace string) bool {
	return !secret && (manifestHas || envHas) && (!manifestHas || manifest == namespace) && (!envHas || env == namespace)
}
func hostedTaskBindingMatchesSource(r HostedTaskBindingRecord, s api.HostedTaskSourceIntent) bool {
	return r.Public.AccountID == s.AccountID && r.Public.IntentID == s.ID && r.Public.IntentSHA256 == s.IntentSHA256 && r.Public.ReviewSHA256 == s.ReviewSHA256 && r.Public.ExpiresAt.Equal(s.ExpiresAt) && r.RequesterKeyID == s.RequesterKeyID
}

type hostedTaskNamespaceSpec struct{ app, key string }

func hostedTaskNamespaceSpecs(roles map[string]string, p tasktoolbinding.Policy) []hostedTaskNamespaceSpec {
	out := []hostedTaskNamespaceSpec{{roles["web_database"], p.WebNamespaceEnv}, {roles["worker_database"], p.WorkerNamespaceEnv}, {roles["observer_database"], "MCP_TASK_NAMESPACE"}}
	// Split runtime roles require this override even when their source names a
	// different namespace env. Never qualify a shadowed configured namespace.
	if p.WebNamespaceEnv != "MCP_TASK_NAMESPACE" {
		out = append(out, hostedTaskNamespaceSpec{roles["web_database"], "MCP_TASK_NAMESPACE"})
	}
	if p.WorkerNamespaceEnv != "MCP_TASK_NAMESPACE" {
		out = append(out, hostedTaskNamespaceSpec{roles["worker_database"], "MCP_TASK_NAMESPACE"})
	}
	return out
}
