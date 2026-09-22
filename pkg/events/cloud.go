package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Envelope is the canonical structured event accepted by the internal event
// router (EPIC #1278, Workstream B). It follows the CloudEvents context
// attributes while retaining Gregale's account_id tenancy extension and the
// public API's snake_case data_content_type spelling.
//
// The API boundary fills SpecVersion, Time, DataContentType, and AccountID;
// callers must provide ID, Source, Type, and a valid JSON Data value.
type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"data_content_type"`
	Data            json.RawMessage `json:"data"`
	AccountID       string          `json:"account_id"`
	// Traceparent, Tracestate, and Baggage are platform-stamped CloudEvents
	// extensions. They are not accepted from the public DTO directly; apid
	// stamps the authenticated publish request's context before persisting the
	// envelope, and schedd uses them to link each fan-out invocation.
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
	Baggage     string `json:"baggage,omitempty"`
}

const (
	CloudEventsSpecVersion = "1.0"
	JSONDataContentType    = "application/json"
	maxEnvelopeString      = 256
)

// Normalize fills server-owned defaults and validates the complete envelope.
// accountID is always authoritative; a caller-supplied account_id must match
// it so a publish can never cross tenant boundaries.
func (e Envelope) Normalize(accountID string, now time.Time) (Envelope, error) {
	e.ID = strings.TrimSpace(e.ID)
	e.Source = strings.TrimSpace(e.Source)
	e.Type = strings.TrimSpace(e.Type)
	e.AccountID = strings.TrimSpace(e.AccountID)
	if e.SpecVersion == "" {
		e.SpecVersion = CloudEventsSpecVersion
	}
	if e.DataContentType == "" {
		e.DataContentType = JSONDataContentType
	}
	if e.Time.IsZero() {
		if now.IsZero() {
			now = time.Now()
		}
		e.Time = now
	}
	e.Time = e.Time.UTC()

	if e.AccountID != "" && e.AccountID != accountID {
		return Envelope{}, fmt.Errorf("account_id must match the authenticated account")
	}
	e.AccountID = accountID
	if err := e.Validate(); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

// Validate checks the stable, transport-independent event contract.
func (e Envelope) Validate() error {
	if e.SpecVersion != CloudEventsSpecVersion {
		return fmt.Errorf("specversion must be %q", CloudEventsSpecVersion)
	}
	if e.ID == "" || len(e.ID) > maxEnvelopeString {
		return errors.New("id is required and must be at most 256 characters")
	}
	if e.Source == "" || len(e.Source) > maxEnvelopeString {
		return errors.New("source is required and must be at most 256 characters")
	}
	if e.Type == "" || len(e.Type) > maxEnvelopeString {
		return errors.New("type is required and must be at most 256 characters")
	}
	if e.DataContentType != JSONDataContentType {
		return fmt.Errorf("data_content_type must be %q", JSONDataContentType)
	}
	if _, err := uuid.Parse(e.AccountID); err != nil {
		return errors.New("account_id must be a UUID")
	}
	if len(e.Data) == 0 || !json.Valid(e.Data) {
		return errors.New("data must be valid JSON")
	}
	if e.Time.IsZero() {
		return errors.New("time is required")
	}
	return nil
}
