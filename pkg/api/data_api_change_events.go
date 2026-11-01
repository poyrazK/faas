package api

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// DataAPIChangeEventKeyPrefix namespaces change hook producer keys
// (ADR-20261022121642) inside the Data API app's event source.
const DataAPIChangeEventKeyPrefix = "data-api.change."

// DataAPIChangeEvent is one recorded row change a Data API runtime publishes.
// ID is the change's UUID in the database; the platform derives the producer
// key from it, so republishing a change deduplicates.
type DataAPIChangeEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Time *time.Time      `json:"time,omitempty"`
	Data json.RawMessage `json:"data"`
}

// DataAPIChangeEventBatch is a runtime's private publication of recorded
// changes, in the order they were recorded.
type DataAPIChangeEventBatch struct {
	Version int                  `json:"version"`
	Events  []DataAPIChangeEvent `json:"events"`
}

// DataAPIChangeEventBatchResponse reports each event at its input position.
// Publication stops at the first event that may succeed later; later events
// are rejected as retryable without being attempted.
type DataAPIChangeEventBatchResponse struct {
	Source  string                    `json:"source"`
	Results []PublishEventBatchResult `json:"results"`
}

var dataAPIChangeEventID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Validate checks one change event's identity, type and data bounds. Types
// are SCHEMA.TABLE.inserted|updated|deleted.
func (e DataAPIChangeEvent) Validate() error {
	if !dataAPIChangeEventID.MatchString(e.ID) {
		return errors.New("id must be a lowercase UUID")
	}
	if len(e.Type) > 256 || !strings.HasSuffix(e.Type, ".inserted") && !strings.HasSuffix(e.Type, ".updated") && !strings.HasSuffix(e.Type, ".deleted") {
		return errors.New("type must be SCHEMA.TABLE.inserted, .updated or .deleted, at most 256 bytes")
	}
	for _, b := range []byte(e.Type) {
		if b < 0x21 || b > 0x7e {
			return errors.New("type must be printable ASCII without spaces")
		}
	}
	if len(e.Data) == 0 || len(e.Data) > DataAPIChangeEventDataMaxBytes || !json.Valid(e.Data) {
		return errors.New("data must be a JSON value of at most 64 KiB")
	}
	return nil
}
