// adr: 712
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

type entityConfigProvider struct {
	objectstorage.Provider
	*entityTestBucket
	denyDelete bool
	denyWrite  bool
	deletes    int
}

func (p *entityConfigProvider) ReadStateObject(ctx context.Context, _, key string, limit int64) ([]byte, string, error) {
	body, etag, err := p.Get(ctx, key, limit)
	if errors.Is(err, durableentity.ErrNotFound) {
		err = objectstorage.ErrNotFound
	}
	return body, etag, err
}

func (p *entityConfigProvider) WriteStateObject(ctx context.Context, _, key string, body []byte, etag string) (string, error) {
	if p.denyWrite {
		return "", errors.New("private-provider-secret must not appear in startup errors")
	}
	version, err := p.Put(ctx, key, body, etag)
	if errors.Is(err, durableentity.ErrConflict) {
		err = objectstorage.ErrPreconditionFailed
	}
	return version, err
}

func (p *entityConfigProvider) ListObjectsDelimited(ctx context.Context, _, prefix, delimiter, cursor string, limit int32) (objectstorage.ObjectPage, error) {
	if delimiter != "/" {
		return objectstorage.ObjectPage{}, errors.New("unexpected delimiter")
	}
	page, err := p.ListEntityPrefixes(ctx, prefix, cursor, limit)
	return objectstorage.ObjectPage{CommonPrefixes: page.Prefixes, NextCursor: page.NextCursor}, err
}

func (p *entityConfigProvider) ListObjects(ctx context.Context, _, prefix, cursor string, limit int32) (objectstorage.ObjectPage, error) {
	page, err := p.ListEntityObjects(ctx, prefix, cursor, limit)
	result := objectstorage.ObjectPage{NextCursor: page.NextCursor}
	for _, key := range page.Keys {
		result.Items = append(result.Items, objectstorage.Object{Key: key})
	}
	return result, err
}

func (p *entityConfigProvider) DeleteObject(ctx context.Context, _, key string) error {
	if !strings.HasPrefix(key, "gregale/durable-entities/v1/probes/maintenance/") {
		return errors.New("configuration probe touched real state")
	}
	p.deletes++
	if p.denyDelete {
		return errors.New("private-provider-secret must not appear in startup errors")
	}
	return p.DeleteEntityObject(ctx, key)
}

func TestDurableEntityMaintenanceStartupChecksDeleteOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, failure := range []string{"none", "delete", "write"} {
			t.Run(strings.Join([]string{boolLabel(enabled), failure}, "/"), func(t *testing.T) {
				provider := &entityConfigProvider{entityTestBucket: &entityTestBucket{objects: map[string]entityTestObject{}}, denyDelete: failure == "delete", denyWrite: failure == "write"}
				registry, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "private"}, Backends: []objectstorage.BackendConfig{{ID: "private", Driver: "s3", Region: "us-east-1", Namespace: "entities", Endpoint: "https://private.example.test", S3Region: "us-east-1"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{
					"s3": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
						return provider, nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				backend, err := registry.Default("us-east-1")
				if err != nil {
					t.Fatal(err)
				}
				config := map[string]string{"FAAS_DURABLE_ENTITIES_ENABLED": "1", "FAAS_DURABLE_ENTITY_BACKEND": "private", "FAAS_DURABLE_ENTITY_BACKEND_FINGERPRINT": backend.Fingerprint, "FAAS_DURABLE_ENTITY_BUCKET": "private-entities", "FAAS_DURABLE_ENTITY_APPS": uuid.NewString()}
				if enabled {
					config["FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED"] = "1"
				}
				s := &server{objectStorage: registry}
				err = s.configureDurableEntities(t.Context(), func(key string) string { return config[key] })
				if enabled && provider.denyDelete || provider.denyWrite {
					if err == nil || strings.Contains(err.Error(), "private-provider-secret") || s.durableEntities != nil {
						t.Fatal("failed probe leaked details or enabled the engine", err)
					}
				} else if err != nil || s.durableEntities == nil || s.durableEntityMaintenanceEnabled != enabled {
					t.Fatal("configuration gates failed", err)
				}
				expectedDeletes := boolInt(enabled && !provider.denyWrite)
				if provider.deletes != expectedDeletes {
					t.Fatal("base invocation required DELETE", provider.deletes)
				}
				if s.durableEntities != nil {
					assertConfiguredEntityLeaseBudgets(t, s.durableEntities, provider.entityTestBucket)
				}
			})
		}
	}
}

func assertConfiguredEntityLeaseBudgets(t *testing.T, m *durableentity.Manager, bucket *entityTestBucket) {
	t.Helper()
	id := durableentity.ID{AccountID: "account", AppID: "app", Namespace: "counters", Key: "configured"}
	started := time.Now()
	claim, err := m.Acquire(t.Context(), id, "maintenance")
	if err != nil || claim.ExpiresAt.Before(started.Add(api.MaxDurableEntityLease)) {
		t.Fatal("maintenance lost its longer lease", claim.ExpiresAt, err)
	}
	if err := m.Release(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	_, err = m.Invoke(t.Context(), id, "caller", durableentity.Request{ID: "one", Payload: json.RawMessage(`{}`)}, func(context.Context, durableentity.View) (durableentity.Transition, error) {
		bucket.mu.Lock()
		defer bucket.mu.Unlock()
		for key, object := range bucket.objects {
			if !strings.HasSuffix(key, "/manifest.json") {
				continue
			}
			var value struct {
				ExpiresAt time.Time `json:"expires_at"`
			}
			if err := json.Unmarshal(object.body, &value); err != nil {
				return durableentity.Transition{}, err
			}
			if remaining := time.Until(value.ExpiresAt); remaining <= 0 || remaining > api.DurableEntityInvocationLease {
				t.Error("runtime invocation did not use the short lease", remaining)
			}
		}
		return durableentity.Transition{Data: json.RawMessage(`{}`), Result: json.RawMessage(`1`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func boolLabel(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
