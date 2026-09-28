package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

var eventSchemaVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var (
	ErrEventSchemaRequired = errors.New("event schema version is required")
	ErrEventSchemaUnknown  = errors.New("event schema version is not registered")
	ErrEventDataInvalid    = errors.New("event data does not match its schema")
)

type EventSchema struct {
	AccountID string          `json:"account_id"`
	Source    string          `json:"source"`
	Type      string          `json:"type"`
	Version   string          `json:"version"`
	Schema    json.RawMessage `json:"schema"`
	CreatedAt time.Time       `json:"created_at"`
}

type EventSchemaStore interface {
	PutEventSchema(context.Context, EventSchema) (bool, error)
	ListEventSchemasForSourceType(context.Context, string, string, string) ([]EventSchema, error)
	LookupEventSchemaForPublish(context.Context, string, string, string, string) (bool, json.RawMessage, error)
}

var compiledEventSchemaCache = edgevalidate.NewCache()

func ValidateEventSchemaDefinition(def EventSchema) error {
	if def.AccountID == "" || def.Source == "" || len(def.Source) > 256 || def.Type == "" || len(def.Type) > 256 || !eventSchemaVersionPattern.MatchString(def.Version) {
		return fmt.Errorf("event schema requires account, source, type, and an alphanumeric version of at most 64 characters")
	}
	_, err := edgevalidate.Compile(def.Schema, false)
	return err
}

func eventSchemaKey(accountID, source, typ, version string) string {
	key, _ := json.Marshal([4]string{accountID, source, typ, version})
	return string(key)
}

func (s *PgStore) PutEventSchema(ctx context.Context, def EventSchema) (bool, error) {
	if err := ValidateEventSchemaDefinition(def); err != nil {
		return false, err
	}
	result, err := s.pool.Exec(ctx, `INSERT INTO event_schemas (account_id, source, event_type, version, schema)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb) ON CONFLICT DO NOTHING`,
		def.AccountID, def.Source, def.Type, def.Version, def.Schema)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() == 1 {
		return true, nil
	}
	var existing []byte
	err = s.pool.QueryRow(ctx, `SELECT schema FROM event_schemas WHERE account_id=$1::uuid AND source=$2 AND event_type=$3 AND version=$4`,
		def.AccountID, def.Source, def.Type, def.Version).Scan(&existing)
	if err != nil {
		return false, err
	}
	if !jsonEqual(existing, def.Schema) {
		return false, ErrConflict
	}
	return false, nil
}

func (s *PgStore) ListEventSchemasForSourceType(ctx context.Context, accountID, source, typ string) ([]EventSchema, error) {
	rows, err := s.pool.Query(ctx, `SELECT version, schema, created_at FROM event_schemas
		WHERE account_id=$1::uuid AND source=$2 AND event_type=$3 ORDER BY version LIMIT 1000`, accountID, source, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventSchema
	for rows.Next() {
		def := EventSchema{AccountID: accountID, Source: source, Type: typ}
		if err := rows.Scan(&def.Version, &def.Schema, &def.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, rows.Err()
}

func (s *PgStore) LookupEventSchemaForPublish(ctx context.Context, accountID, source, typ, version string) (bool, json.RawMessage, error) {
	var required bool
	var schema []byte
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM event_schemas WHERE account_id=$1::uuid AND source=$2 AND event_type=$3),
		(SELECT schema FROM event_schemas WHERE account_id=$1::uuid AND source=$2 AND event_type=$3 AND version=$4)`,
		accountID, source, typ, version).Scan(&required, &schema)
	return required, schema, err
}

func (m *MemStore) PutEventSchema(_ context.Context, def EventSchema) (bool, error) {
	if err := ValidateEventSchemaDefinition(def); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.eventSchemas == nil {
		m.eventSchemas = make(map[string]EventSchema)
	}
	key := eventSchemaKey(def.AccountID, def.Source, def.Type, def.Version)
	if prior, ok := m.eventSchemas[key]; ok {
		if !jsonEqual(prior.Schema, def.Schema) {
			return false, ErrConflict
		}
		return false, nil
	}
	def.Schema = bytes.Clone(def.Schema)
	def.CreatedAt = time.Now().UTC()
	m.eventSchemas[key] = def
	return true, nil
}

func (m *MemStore) ListEventSchemasForSourceType(_ context.Context, accountID, source, typ string) ([]EventSchema, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EventSchema
	for _, def := range m.eventSchemas {
		if def.AccountID == accountID && def.Source == source && def.Type == typ {
			def.Schema = bytes.Clone(def.Schema)
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func (m *MemStore) LookupEventSchemaForPublish(_ context.Context, accountID, source, typ, version string) (bool, json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var required bool
	var schema json.RawMessage
	for _, def := range m.eventSchemas {
		if def.AccountID != accountID || def.Source != source || def.Type != typ {
			continue
		}
		required = true
		if def.Version == version {
			schema = bytes.Clone(def.Schema)
		}
	}
	return required, schema, nil
}

// ValidatePublishedEventSchema applies the immutable account/source/type
// contract when one exists. Unregistered event types remain schema-free.
func ValidatePublishedEventSchema(ctx context.Context, store EventSchemaStore, accountID, source, typ, version string, data json.RawMessage) error {
	if version != "" && !eventSchemaVersionPattern.MatchString(version) {
		return ErrEventSchemaUnknown
	}
	required, schema, err := store.LookupEventSchemaForPublish(ctx, accountID, source, typ, version)
	if err != nil {
		return err
	}
	if !required {
		if version != "" {
			return ErrEventSchemaUnknown
		}
		return nil
	}
	if version == "" {
		return ErrEventSchemaRequired
	}
	if len(schema) == 0 {
		return ErrEventSchemaUnknown
	}
	digest := sha256.Sum256(schema)
	compiled, ok := compiledEventSchemaCache.Get(digest)
	if !ok {
		compiled, err = edgevalidate.Compile(schema, false)
		if err != nil {
			return err
		}
		compiledEventSchemaCache.Register(digest, compiled)
	}
	field, err := compiled.Validate(data)
	if err != nil {
		return err
	}
	if field != nil {
		return fmt.Errorf("%w: field=%q expected=%q got=%q", ErrEventDataInvalid, field.Field, field.Expected, field.Got)
	}
	return nil
}
