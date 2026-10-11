package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Schema is optional: omission checks an existing immutable registered version.
type EventSchemaRolloutRequest struct {
	Source        string            `json:"source"`
	Type          string            `json:"type"`
	Version       string            `json:"version"`
	Schema        json.RawMessage   `json:"schema,omitempty"`
	Samples       []json.RawMessage `json:"samples,omitempty"`
	From          *time.Time        `json:"from,omitempty"`
	Until         *time.Time        `json:"until,omitempty"`
	RetainedLimit int               `json:"retained_limit,omitempty"`
}

func (r EventSchemaRolloutRequest) Validate() error {
	if r.Source == "" || r.Type == "" || len(r.Source) > EventSchemaRolloutIdentityMaxBytes || len(r.Type) > EventSchemaRolloutIdentityMaxBytes || strings.TrimSpace(r.Source) != r.Source || strings.TrimSpace(r.Type) != r.Type || strings.ContainsAny(r.Source+r.Type, "*") {
		return fmt.Errorf("source and type must be concrete nonempty identifiers of at most 256 bytes")
	}
	if _, err := NormalizeEventSchemaVersions([]string{r.Version}); err != nil {
		return err
	}
	if len(r.Schema) > EventSchemaRolloutSchemaMaxBytes || len(r.Schema) > 0 && !json.Valid(r.Schema) {
		return fmt.Errorf("schema must be valid JSON of at most %d bytes", EventSchemaRolloutSchemaMaxBytes)
	}
	if len(r.Samples) > EventSchemaRolloutSamplesMax {
		return fmt.Errorf("provide at most %d samples", EventSchemaRolloutSamplesMax)
	}
	for _, sample := range r.Samples {
		if len(sample) == 0 || len(sample) > EventSchemaRolloutSampleMaxBytes || !json.Valid(sample) {
			return fmt.Errorf("each sample must be valid JSON of at most %d bytes", EventSchemaRolloutSampleMaxBytes)
		}
	}
	if (r.From == nil) != (r.Until == nil) || r.From != nil && (r.From.IsZero() || r.Until.IsZero() || !r.From.Before(*r.Until)) {
		return fmt.Errorf("from and until must define a nonempty acceptance-time range")
	}
	if r.RetainedLimit < 0 || r.RetainedLimit > EventSchemaRolloutRetainedMax || r.RetainedLimit != 0 && r.From == nil {
		return fmt.Errorf("retained_limit requires a range and must be between 1 and %d", EventSchemaRolloutRetainedMax)
	}
	return nil
}

type EventSchemaRolloutConsumer struct {
	SubscriptionID       string   `json:"subscription_id"`
	AppID                string   `json:"app_id"`
	Source               string   `json:"source"`
	Type                 string   `json:"type"`
	SchemaVersions       []string `json:"schema_versions"`
	AcceptsVersion       bool     `json:"accepts_version"`
	ContentFilterPresent bool     `json:"content_filter_present"`
}
type EventSchemaRolloutValidation struct {
	SampleIndex *int       `json:"sample_index,omitempty"`
	EventID     string     `json:"event_id,omitempty"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	Valid       bool       `json:"valid"`
	Reason      string     `json:"reason,omitempty"`
	Field       string     `json:"field,omitempty"`
}
type EventSchemaRolloutRetained struct {
	Requested       bool                           `json:"requested"`
	From            *time.Time                     `json:"from,omitempty"`
	Until           *time.Time                     `json:"until,omitempty"`
	CutoffAt        *time.Time                     `json:"cutoff_at,omitempty"`
	ScannedCount    int                            `json:"scanned_count"`
	ExaminedCount   int                            `json:"examined_count"`
	ValidCount      int                            `json:"valid_count"`
	InvalidCount    int                            `json:"invalid_count"`
	UnreadableCount int                            `json:"unreadable_count"`
	Truncated       bool                           `json:"truncated"`
	HistoryComplete bool                           `json:"history_complete"`
	Results         []EventSchemaRolloutValidation `json:"results"`
}
type EventSchemaRolloutResponse struct {
	Source             string                         `json:"source"`
	Type               string                         `json:"type"`
	Version            string                         `json:"version"`
	SchemaOrigin       string                         `json:"schema_origin"`
	SchemaDigest       string                         `json:"schema_digest"`
	ObservedAt         time.Time                      `json:"observed_at"`
	ConsumerCount      int                            `json:"consumer_count"`
	AcceptingCount     int                            `json:"accepting_count"`
	ExcludingCount     int                            `json:"excluding_count"`
	ConsumersTruncated bool                           `json:"consumers_truncated"`
	Consumers          []EventSchemaRolloutConsumer   `json:"consumers"`
	SampleValidCount   int                            `json:"sample_valid_count"`
	SampleInvalidCount int                            `json:"sample_invalid_count"`
	Samples            []EventSchemaRolloutValidation `json:"samples"`
	Retained           EventSchemaRolloutRetained     `json:"retained"`
}

func (c *Client) PreviewEventSchemaRollout(ctx context.Context, req EventSchemaRolloutRequest) (EventSchemaRolloutResponse, error) {
	var out EventSchemaRolloutResponse
	err := c.do(ctx, http.MethodPost, "/v1/event-schemas:preview-rollout", req, &out)
	return out, err
}
