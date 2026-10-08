package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jsonschemautil"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"strconv"
	"time"
)

const ManagedRealtimeEventSchemaMaxBytes = 16 << 10
const ManagedRealtimeEventSchemaMaxPerEndpoint = 64

var ErrManagedRealtimeEventSchemaLimit = errors.New("state: event schema limit reached")

type ManagedRealtimeEventSchemaError struct {
	Item         int
	Path, Reason string
}

func (e *ManagedRealtimeEventSchemaError) Error() string {
	prefix := "event"
	if e.Item >= 0 {
		prefix = fmt.Sprintf("messages[%d]", e.Item)
	}
	return fmt.Sprintf("%s at %s: %s", prefix, e.Path, e.Reason)
}

type ManagedRealtimeEventSchema struct {
	EndpointID string          `json:"-"`
	Channel    string          `json:"channel"`
	EventType  string          `json:"event_type"`
	Version    int             `json:"version"`
	Schema     json.RawMessage `json:"schema"`
	CreatedAt  time.Time       `json:"created_at"`
}
type managedRealtimeEventSchemaKey struct {
	endpointID, channel, eventType string
	version                        int
}
type ManagedRealtimeEventSchemaStore interface {
	PutManagedRealtimeEventSchema(context.Context, ManagedRealtimeEventSchema) (ManagedRealtimeEventSchema, error)
	GetManagedRealtimeEventSchema(context.Context, string, string, string, int) (ManagedRealtimeEventSchema, error)
}
type realtimeSchemaLoader struct{}

func (realtimeSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("external schema references are unsupported")
}
func compileRealtimeEventSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) == 0 || len(raw) > ManagedRealtimeEventSchemaMaxBytes {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	doc, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if decodeErr != nil || len(canonicalEventSchema(raw)) > ManagedRealtimeEventSchemaMaxBytes {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	if obj, ok := doc.(map[string]any); ok {
		if draft, exists := obj["$schema"]; exists && draft != "https://json-schema.org/draft/2020-12/schema" {
			return nil, ErrManagedRealtimeHistoryInvalid
		}
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(realtimeSchemaLoader{})
	const location = "https://gregale.dev/schemas/realtime-event.json"
	if err := c.AddResource(location, doc); err != nil {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	compiled, err := c.Compile(location)
	if err != nil {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	return compiled, nil
}
func validateEventSchemaKey(ep, ch, event string, version int) error {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || api.ValidateRealtimeNotificationCategory(event) != nil || version < 1 || version > 1000000 {
		return ErrManagedRealtimeHistoryInvalid
	}
	return nil
}
func canonicalEventSchema(raw json.RawMessage) []byte {
	value, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	out, _ := json.Marshal(value)
	return out
}
func validateTypedRealtimeEvent(raw json.RawMessage, data []byte, binary bool) error {
	fail := func(path, reason string) error {
		return &ManagedRealtimeEventSchemaError{Item: -1, Path: path, Reason: reason}
	}
	if binary {
		return fail("/binary", "schema-bound events must be JSON")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !json.Valid(data) || decoder.Decode(&value) != nil {
		return fail("/data_base64", "decoded event must be valid JSON")
	}
	compiled, err := compileRealtimeEventSchema(raw)
	if err != nil {
		return err
	}
	if err = compiled.Validate(value); err != nil {
		var detail *jsonschema.ValidationError
		if errors.As(err, &detail) {
			for len(detail.Causes) > 0 {
				detail = detail.Causes[0]
			}
			path := jsonschemautil.JoinInstanceLocation(detail.InstanceLocation)
			if path == "" {
				path = "/"
			}
			reason := "does not match the declared event schema"
			if detail.ErrorKind != nil {
				localized := detail.ErrorKind.LocalizedString(jsonschemautil.DefaultPrinter)
				if localized != "" && len(localized) <= 512 {
					reason = localized
				}
			}
			return fail(path, reason)
		}
		return fail("/", "does not match the declared event schema")
	}
	return nil
}
func eventSchemaSelection(metadata map[string]string) (string, int, error) {
	event := metadata["event_type"]
	version, err := strconv.Atoi(metadata["schema_version"])
	if api.ValidateRealtimeNotificationCategory(event) != nil || err != nil || version < 1 || version > 1000000 || strconv.Itoa(version) != metadata["schema_version"] {
		return "", 0, &ManagedRealtimeEventSchemaError{Item: -1, Path: "/metadata", Reason: "event_type and canonical schema_version are required"}
	}
	return event, version, nil
}
func (m *MemStore) validateEventSchemaLocked(ep, ch string, data []byte, binary bool, metadata map[string]string) error {
	configured := false
	for key := range m.managedRealtimeEventSchemas {
		if key.endpointID == ep && key.channel == ch {
			configured = true
			break
		}
	}
	if !configured && metadata["schema_version"] == "" {
		return nil
	}
	event, version, err := eventSchemaSelection(metadata)
	if err != nil {
		return err
	}
	row, ok := m.managedRealtimeEventSchemas[managedRealtimeEventSchemaKey{ep, ch, event, version}]
	if !ok {
		return &ManagedRealtimeEventSchemaError{Item: -1, Path: "/metadata/schema_version", Reason: "event schema version is not registered"}
	}
	return validateTypedRealtimeEvent(row.Schema, data, binary)
}
func validateEventSchemaPG(ctx context.Context, tx pgx.Tx, ep, ch string, data []byte, binary bool, metadata map[string]string) error {
	var configured bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_event_schemas where endpoint_id=$1 and channel=$2)`, ep, ch).Scan(&configured); err != nil {
		return err
	}
	if !configured && metadata["schema_version"] == "" {
		return nil
	}
	event, version, err := eventSchemaSelection(metadata)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	err = tx.QueryRow(ctx, `select schema from managed_realtime_event_schemas where endpoint_id=$1 and channel=$2 and event_type=$3 and version=$4`, ep, ch, event, version).Scan(&raw)
	if err == pgx.ErrNoRows {
		return &ManagedRealtimeEventSchemaError{Item: -1, Path: "/metadata/schema_version", Reason: "event schema version is not registered"}
	}
	if err != nil {
		return err
	}
	return validateTypedRealtimeEvent(raw, data, binary)
}
func (m *MemStore) PutManagedRealtimeEventSchema(ctx context.Context, row ManagedRealtimeEventSchema) (ManagedRealtimeEventSchema, error) {
	if err := validateEventSchemaKey(row.EndpointID, row.Channel, row.EventType, row.Version); err != nil {
		return row, err
	}
	if _, err := compileRealtimeEventSchema(row.Schema); err != nil {
		return row, err
	}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[row.EndpointID]; !ok {
		return row, ErrNotFound
	}
	key := managedRealtimeEventSchemaKey{row.EndpointID, row.Channel, row.EventType, row.Version}
	if old, ok := m.managedRealtimeEventSchemas[key]; ok {
		if !bytes.Equal(canonicalEventSchema(old.Schema), canonicalEventSchema(row.Schema)) {
			return row, ErrConflict
		}
		old.Schema = append(json.RawMessage(nil), old.Schema...)
		return old, nil
	}
	count := 0
	for key := range m.managedRealtimeEventSchemas {
		if key.endpointID == row.EndpointID {
			count++
		}
	}
	if count >= 64 {
		return row, ErrManagedRealtimeEventSchemaLimit
	}
	if m.managedRealtimeEventSchemas == nil {
		m.managedRealtimeEventSchemas = map[managedRealtimeEventSchemaKey]ManagedRealtimeEventSchema{}
	}
	row.CreatedAt = time.Now().UTC()
	row.Schema = canonicalEventSchema(row.Schema)
	m.managedRealtimeEventSchemas[key] = row
	row.Schema = append(json.RawMessage(nil), row.Schema...)
	return row, nil
}
func (m *MemStore) GetManagedRealtimeEventSchema(ctx context.Context, ep, ch, event string, version int) (ManagedRealtimeEventSchema, error) {
	if err := validateEventSchemaKey(ep, ch, event, version); err != nil {
		return ManagedRealtimeEventSchema{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeEventSchema{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.managedRealtimeEventSchemas[managedRealtimeEventSchemaKey{ep, ch, event, version}]
	if !ok {
		return row, ErrNotFound
	}
	row.Schema = append(json.RawMessage(nil), row.Schema...)
	return row, nil
}
func (s *PgStore) PutManagedRealtimeEventSchema(ctx context.Context, row ManagedRealtimeEventSchema) (ManagedRealtimeEventSchema, error) {
	if err := validateEventSchemaKey(row.EndpointID, row.Channel, row.EventType, row.Version); err != nil {
		return row, err
	}
	if _, err := compileRealtimeEventSchema(row.Schema); err != nil {
		return row, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return row, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, row.EndpointID).Scan(&id); err != nil {
		if err == pgx.ErrNoRows {
			err = ErrNotFound
		}
		return row, err
	}
	// Serializes enabling channel enforcement with appends and message edits.
	if _, err = tx.Exec(ctx, `select next_sequence from managed_realtime_channel_heads where endpoint_id=$1 and channel=$2 for update`, row.EndpointID, row.Channel); err != nil {
		return row, err
	}
	var old json.RawMessage
	err = tx.QueryRow(ctx, `select schema,created_at from managed_realtime_event_schemas where endpoint_id=$1 and channel=$2 and event_type=$3 and version=$4`, row.EndpointID, row.Channel, row.EventType, row.Version).Scan(&old, &row.CreatedAt)
	if err == nil {
		if !bytes.Equal(canonicalEventSchema(old), canonicalEventSchema(row.Schema)) {
			return row, ErrConflict
		}
		row.Schema = old
		return row, tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return row, err
	}
	var count int
	if err = tx.QueryRow(ctx, `select count(*) from managed_realtime_event_schemas where endpoint_id=$1`, row.EndpointID).Scan(&count); err != nil {
		return row, err
	}
	if count >= 64 {
		return row, ErrManagedRealtimeEventSchemaLimit
	}
	row.Schema = canonicalEventSchema(row.Schema)
	err = tx.QueryRow(ctx, `insert into managed_realtime_event_schemas(endpoint_id,channel,event_type,version,schema) values($1,$2,$3,$4,$5) returning created_at`, row.EndpointID, row.Channel, row.EventType, row.Version, row.Schema).Scan(&row.CreatedAt)
	if err != nil {
		return row, err
	}
	return row, tx.Commit(ctx)
}
func (s *PgStore) GetManagedRealtimeEventSchema(ctx context.Context, ep, ch, event string, version int) (ManagedRealtimeEventSchema, error) {
	row := ManagedRealtimeEventSchema{EndpointID: ep, Channel: ch, EventType: event, Version: version}
	if err := validateEventSchemaKey(ep, ch, event, version); err != nil {
		return row, err
	}
	err := s.pool.QueryRow(ctx, `select schema,created_at from managed_realtime_event_schemas where endpoint_id=$1 and channel=$2 and event_type=$3 and version=$4`, ep, ch, event, version).Scan(&row.Schema, &row.CreatedAt)
	if err == pgx.ErrNoRows {
		err = ErrNotFound
	}
	return row, err
}

func ValidateManagedRealtimeEventSchema(raw json.RawMessage) error {
	_, err := compileRealtimeEventSchema(raw)
	return err
}
