package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
	"github.com/onebox-faas/faas/pkg/eventcontract"
)

type EventSchemaRolloutStore interface {
	PreviewEventSchemaRollout(context.Context, string, api.EventSchemaRolloutRequest) (api.EventSchemaRolloutResponse, error)
}
type eventSchemaRolloutBackend interface {
	EventSchemaStore
	EventSubscriptionMatcherStore
	eventSchemaRolloutRetained(context.Context, string, api.EventSchemaRolloutRequest, time.Time) (eventSchemaRolloutObservation, error)
}
type eventSchemaRolloutRow struct {
	id       string
	accepted time.Time
	payload  []byte
}
type eventSchemaRolloutObservation struct {
	scanned   int
	truncated bool
	rows      []eventSchemaRolloutRow
}

func (s *PgStore) PreviewEventSchemaRollout(ctx context.Context, account string, req api.EventSchemaRolloutRequest) (api.EventSchemaRolloutResponse, error) {
	return previewEventSchemaRollout(ctx, s, account, req)
}
func (m *MemStore) PreviewEventSchemaRollout(ctx context.Context, account string, req api.EventSchemaRolloutRequest) (api.EventSchemaRolloutResponse, error) {
	return previewEventSchemaRollout(ctx, m, account, req)
}

func previewEventSchemaRollout(ctx context.Context, store eventSchemaRolloutBackend, account string, req api.EventSchemaRolloutRequest) (api.EventSchemaRolloutResponse, error) {
	out := api.EventSchemaRolloutResponse{Source: req.Source, Type: req.Type, Version: req.Version, ObservedAt: time.Now().UTC(), Consumers: []api.EventSchemaRolloutConsumer{}, Samples: []api.EventSchemaRolloutValidation{}, Retained: api.EventSchemaRolloutRetained{Results: []api.EventSchemaRolloutValidation{}}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if _, err := uuid.Parse(account); err != nil {
		return out, ErrInvalidArgument
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}
	schema := req.Schema
	out.SchemaOrigin = "proposed"
	if len(schema) == 0 {
		_, registered, err := store.LookupEventSchemaForPublish(ctx, account, req.Source, req.Type, req.Version)
		if err != nil {
			return out, err
		}
		if len(registered) == 0 {
			return out, ErrEventSchemaUnknown
		}
		schema = registered
		out.SchemaOrigin = "registered"
	}
	compiled, err := edgevalidate.Compile(schema, false)
	if err != nil {
		return out, fmt.Errorf("%w: invalid schema definition", ErrInvalidArgument)
	}
	digest := sha256.Sum256(schema)
	out.SchemaDigest = hex.EncodeToString(digest[:])
	if err = eventSchemaRolloutConsumers(ctx, store, account, req, &out); err != nil {
		return out, err
	}
	for i, sample := range req.Samples {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		result, err := eventSchemaRolloutValidate(compiled, sample)
		if err != nil {
			return out, err
		}
		index := i
		result.SampleIndex = &index
		if result.Valid {
			out.SampleValidCount++
		} else {
			out.SampleInvalidCount++
		}
		out.Samples = append(out.Samples, result)
	}
	if req.From != nil {
		cutoff := minTime(out.ObservedAt, *req.Until)
		if !req.From.Before(cutoff) {
			return out, fmt.Errorf("%w: from must precede the observation cutoff", ErrInvalidArgument)
		}
		observed, err := store.eventSchemaRolloutRetained(ctx, account, req, cutoff)
		if err != nil {
			return out, err
		}
		out.Retained, err = eventSchemaRolloutValidateRetained(ctx, compiled, account, req, cutoff, observed)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
func eventSchemaRolloutConsumers(ctx context.Context, store EventSubscriptionMatcherStore, account string, req api.EventSchemaRolloutRequest, out *api.EventSchemaRolloutResponse) error {
	rows, err := store.ListMatchingEventSubscriptionsForAccount(ctx, account, req.Source, req.Type, EventSubscriptionCursor{}, api.EventSchemaRolloutConsumersMax)
	if err != nil {
		return err
	}
	if len(rows) == api.EventSchemaRolloutConsumersMax {
		last := rows[len(rows)-1]
		more, err := store.ListMatchingEventSubscriptionsForAccount(ctx, account, req.Source, req.Type, EventSubscriptionCursor{CreatedAt: last.CreatedAt, ID: last.ID}, 1)
		if err != nil {
			return err
		}
		out.ConsumersTruncated = len(more) > 0
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		accepts := len(row.SchemaVersions) == 0 || slices.Contains(row.SchemaVersions, req.Version)
		filter := bytes.TrimSpace(row.Filter)
		out.Consumers = append(out.Consumers, api.EventSchemaRolloutConsumer{SubscriptionID: row.ID, AppID: row.AppID, Source: row.Source, Type: row.Type, SchemaVersions: append([]string{}, row.SchemaVersions...), AcceptsVersion: accepts, ContentFilterPresent: len(filter) > 0 && !bytes.Equal(filter, []byte("null")) && !bytes.Equal(filter, []byte("{}"))})
		out.ConsumerCount++
		if accepts {
			out.AcceptingCount++
		} else {
			out.ExcludingCount++
		}
	}
	return nil
}
func eventSchemaRolloutValidate(compiled *edgevalidate.CompiledSchema, data []byte) (api.EventSchemaRolloutValidation, error) {
	field, err := compiled.Validate(data)
	if err != nil {
		return api.EventSchemaRolloutValidation{}, err
	}
	result := api.EventSchemaRolloutValidation{Valid: field == nil}
	if field != nil {
		result.Reason = field.Reason()
		result.Field = eventSchemaRolloutDiagnostic(field.Field)
	}
	return result, nil
}
func eventSchemaRolloutDiagnostic(value string) string {
	if len(value) <= api.EventSchemaRolloutDiagnosticMaxBytes {
		return value
	}
	value = value[:api.EventSchemaRolloutDiagnosticMaxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
func eventSchemaRolloutValidateRetained(ctx context.Context, compiled *edgevalidate.CompiledSchema, account string, req api.EventSchemaRolloutRequest, cutoff time.Time, observed eventSchemaRolloutObservation) (api.EventSchemaRolloutRetained, error) {
	out := api.EventSchemaRolloutRetained{Requested: true, From: req.From, Until: req.Until, CutoffAt: &cutoff, ScannedCount: observed.scanned, Truncated: observed.truncated, Results: []api.EventSchemaRolloutValidation{}}
	used := 0
	for _, row := range observed.rows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if used+len(row.payload) > api.EventSchemaRolloutRetainedBytesMax {
			out.Truncated = true
			break
		}
		used += len(row.payload)
		result := api.EventSchemaRolloutValidation{EventID: row.id, AcceptedAt: &row.accepted}
		var envelope eventcontract.Envelope
		if len(row.payload) > api.EventSchemaRolloutSampleMaxBytes || json.Unmarshal(row.payload, &envelope) != nil || envelope.Validate() != nil || !sameMemUUID(envelope.AccountID, account) || envelope.Source != req.Source || envelope.Type != req.Type {
			result.Reason = "unreadable_envelope"
			out.UnreadableCount++
		} else {
			validated, err := eventSchemaRolloutValidate(compiled, envelope.Data)
			if err != nil {
				return out, err
			}
			result.Valid, result.Reason, result.Field = validated.Valid, validated.Reason, validated.Field
			if result.Valid {
				out.ValidCount++
			} else {
				out.InvalidCount++
			}
		}
		out.ExaminedCount++
		out.Results = append(out.Results, result)
	}
	return out, nil
}

var _ EventSchemaRolloutStore = (*PgStore)(nil)
var _ EventSchemaRolloutStore = (*MemStore)(nil)
