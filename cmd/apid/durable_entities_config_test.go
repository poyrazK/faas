// adr: 712
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
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
	if !strings.HasPrefix(key, "gregale/durable-entity-backups/v1/probes/") && !strings.HasPrefix(key, "gregale/durable-entities/v1/probes/maintenance/") && !strings.HasPrefix(key, "gregale/durable-entities/v1/probes/alarms/") && !strings.HasPrefix(key, "gregale/durable-entities/v1/probes/outbox/") {
		return errors.New("configuration probe touched real state")
	}
	p.deletes++
	if p.denyDelete {
		return errors.New("private-provider-secret must not appear in startup errors")
	}
	return p.DeleteEntityObject(ctx, key)
}

func TestDurableEntityBackgroundWorkersCheckDeleteOnlyWhenEnabled(t *testing.T) {
	for _, feature := range []string{"MAINTENANCE", "ALARMS", "OUTBOX", "OUTBOX_HANDLERS", "BACKUPS"} {
		for _, enabled := range []bool{false, true} {
			for _, failure := range []string{"none", "delete", "write"} {
				t.Run(strings.Join([]string{feature, boolLabel(enabled), failure}, "/"), func(t *testing.T) {
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
						config["FAAS_DURABLE_ENTITY_"+feature+"_ENABLED"] = "1"
						if feature == "OUTBOX_HANDLERS" {
							config["FAAS_DURABLE_ENTITY_OUTBOX_ENABLED"] = "1"
						}
					}
					s := &server{objectStorage: registry, store: state.NewMemStore()}
					err = s.configureDurableEntities(t.Context(), func(key string) string { return config[key] })
					if enabled && provider.denyDelete || provider.denyWrite {
						if err == nil || strings.Contains(err.Error(), "private-provider-secret") || s.durableEntities != nil {
							t.Fatal("failed probe leaked details or enabled the engine", err)
						}
					} else if err != nil || s.durableEntities == nil || s.durableEntityMaintenanceEnabled != (enabled && feature == "MAINTENANCE") || s.durableEntityAlarmsEnabled != (enabled && feature == "ALARMS") || s.durableEntityOutboxEnabled != (enabled && (feature == "OUTBOX" || feature == "OUTBOX_HANDLERS")) || s.durableEntityOutboxHandlersEnabled != (enabled && feature == "OUTBOX_HANDLERS") || s.durableEntityBackupsEnabled != (enabled && feature == "BACKUPS") {
						t.Fatal("configuration gates failed", err)
					}
					expectedDeletes := boolInt(enabled && !provider.denyWrite)
					if provider.deletes != expectedDeletes {
						t.Fatal("base invocation required DELETE", provider.deletes)
					}
				})
			}
		}
	}

}

func TestDurableEntityOutboxHandlersRequireRelayBeforeProviderAccess(t *testing.T) {
	for _, config := range []map[string]string{
		{"FAAS_DURABLE_ENTITY_OUTBOX_HANDLERS_ENABLED": "1"},
		{"FAAS_DURABLE_ENTITIES_ENABLED": "1", "FAAS_DURABLE_ENTITY_OUTBOX_HANDLERS_ENABLED": "1"},
		{"FAAS_DURABLE_ENTITY_OUTBOX_ENABLED": "1", "FAAS_DURABLE_ENTITY_OUTBOX_HANDLERS_ENABLED": "1"},
	} {
		s := &server{}
		err := s.configureDurableEntities(t.Context(), func(key string) string { return config[key] })
		if err == nil || s.durableEntities != nil || s.durableEntityOutboxHandlersEnabled {
			t.Fatal("unbacked guest messaging was enabled", err)
		}
	}
}

func boolLabel(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func TestRestoreValidationRequiresInvocationPreview(t *testing.T) {
	s := &server{}
	if err := s.configureDurableEntities(t.Context(), func(key string) string {
		if key == "FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil || s.durableEntityRestoreValidationEnabled || s.durableEntities != nil {
		t.Fatal("application validation enabled without invocation preview")
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
