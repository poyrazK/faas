// ADR-939: explicit application-state upgrades inside pure transitions.
package faas

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrDurableEntitySchema = errors.New("faas: invalid, unsupported or incomplete entity state schema")

// Application schema versions are separate from the business commit version.
type DurableEntitySchemaState[State any] struct {
	SchemaVersion uint32 `json:"schema_version"`
	Data          State  `json:"data"`
}

type DurableEntityStateSchema[State any] struct {
	Version  uint32
	Initial  func() State
	Validate func(State) error
	// Migrations[n] is a pure n -> n+1 data transformation.
	Migrations map[uint32]func(json.RawMessage) (json.RawMessage, error)
	// LegacyVersion explicitly admits unwrapped committed data. Nil rejects it.
	LegacyVersion *uint32
}

type DurableEntitySchemaCall[State, Payload any] struct {
	DurableEntityCall[State, Payload]
	StoredSchemaVersion uint32
	Migrated            bool
	current             uint32
	validate            func(State) error
}

func DecodeDurableEntitySchemaCall[State, Payload any](body []byte, schema DurableEntityStateSchema[State]) (DurableEntitySchemaCall[State, Payload], error) {
	var out DurableEntitySchemaCall[State, Payload]
	if schema.Version == 0 || schema.Initial == nil || schema.Validate == nil {
		return out, ErrDurableEntitySchema
	}
	request, err := DecodeDurableEntityHandlerRequest(body)
	if err != nil {
		return out, err
	}
	data := request.State.Data
	source := schema.Version
	legacy := false
	if request.State.Version == 0 {
		data, err = json.Marshal(schema.Initial())
	} else {
		source, data, legacy, err = entitySchemaData(data, schema.LegacyVersion)
	}
	if err != nil {
		return out, fmt.Errorf("read entity schema: %w", err)
	}
	if source > schema.Version || uint64(schema.Version)-uint64(source) > uint64(len(schema.Migrations)) {
		return out, ErrDurableEntitySchema
	}
	steps := make([]func(json.RawMessage) (json.RawMessage, error), 0, uint64(schema.Version)-uint64(source))
	for version := source; version < schema.Version; version++ {
		step := schema.Migrations[version]
		if step == nil {
			return out, ErrDurableEntitySchema
		}
		steps = append(steps, step)
	}
	for index, step := range steps {
		data, err = step(append(json.RawMessage(nil), data...))
		if err != nil {
			return out, fmt.Errorf("migrate entity schema %d: %w", uint64(source)+uint64(index), err)
		}
		if _, err := request.EncodeTransition(DurableEntityTransition{Data: data, Result: json.RawMessage(`null`)}); err != nil {
			return out, err
		}
	}
	call, err := decodeDurableEntityTypedCall[State, Payload](request, data)
	if err != nil {
		return out, err
	}
	if err := schema.Validate(call.State); err != nil {
		return out, fmt.Errorf("validate entity schema: %w", err)
	}
	// Include envelope overhead in negotiated bounds before exposing migrated state.
	encoded, err := json.Marshal(DurableEntitySchemaState[State]{SchemaVersion: schema.Version, Data: call.State})
	if err != nil {
		return out, err
	}
	if _, err := request.EncodeTransition(DurableEntityTransition{Data: encoded, Result: json.RawMessage(`null`)}); err != nil {
		return out, err
	}
	return DurableEntitySchemaCall[State, Payload]{DurableEntityCall: call, StoredSchemaVersion: source, Migrated: request.State.Version > 0 && (source < schema.Version || legacy), current: schema.Version, validate: schema.Validate}, nil
}

func entitySchemaData(raw json.RawMessage, legacy *uint32) (uint32, json.RawMessage, bool, error) {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if version, ok := fields["schema_version"]; ok {
		var n uint32
		if len(fields) != 2 || json.Unmarshal(version, &n) != nil || n == 0 || !json.Valid(fields["data"]) {
			return 0, nil, false, ErrDurableEntitySchema
		}
		return n, append(json.RawMessage(nil), fields["data"]...), false, nil
	}
	if legacy == nil || !json.Valid(raw) {
		return 0, nil, false, ErrDurableEntitySchema
	}
	return *legacy, append(json.RawMessage(nil), raw...), true, nil
}

// Transition always stores the configured current schema with the new state.
// Preparing an upgrade does not persist it; the normal fenced commit does.
func (c DurableEntitySchemaCall[State, Payload]) Transition(data State, result any) *DurableEntityTransitionBuilder[DurableEntitySchemaState[State]] {
	typed := DurableEntityCall[DurableEntitySchemaState[State], Payload]{request: c.request}
	builder := typed.Transition(DurableEntitySchemaState[State]{SchemaVersion: c.current, Data: data}, result)
	builder.validate = func(value DurableEntitySchemaState[State]) error {
		if c.validate == nil || value.SchemaVersion != c.current {
			return ErrDurableEntitySchema
		}
		if err := c.validate(value.Data); err != nil {
			return fmt.Errorf("validate next entity schema: %w", err)
		}
		return nil
	}
	return builder
}
