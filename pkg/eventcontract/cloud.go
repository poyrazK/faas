package eventcontract

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Envelope is the canonical structured event accepted by the internal event
// router (EPIC #1278, Workstream B). It follows the CloudEvents context
// attributes with Gregale's accountid tenancy extension. The decoder accepts
// the previous snake_case spellings for persisted historical envelopes.
//
// The API boundary fills SpecVersion, Time, DataContentType, and AccountID;
// callers must provide ID, Source, Type, and a valid JSON Data value.
type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
	AccountID       string          `json:"accountid"`
	SchemaVersion   string          `json:"schemaversion,omitempty"`
	// Traceparent, Tracestate, and Baggage are platform-stamped CloudEvents
	// extensions. They are not accepted from the public DTO directly; apid
	// stamps the authenticated publish request's context before persisting the
	// envelope, and schedd uses them to link each fan-out invocation.
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
	Baggage     string `json:"baggage,omitempty"`
	// These server-stamped extensions scope customer-published events to the
	// authenticated platform tenant and its linked app.
	AppID            string `json:"appid,omitempty"`
	PlatformTenantID string `json:"platformtenantid,omitempty"`
	TenantEventID    string `json:"tenanteventid,omitempty"`
}

func (e *Envelope) UnmarshalJSON(data []byte) error {
	type wire Envelope
	var decoded struct {
		*wire
		LegacyContentType string `json:"data_content_type"`
		LegacyAccountID   string `json:"account_id"`
	}
	value := wire{}
	decoded.wire = &value
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.LegacyContentType != "" {
		if value.DataContentType != "" && value.DataContentType != decoded.LegacyContentType {
			return errors.New("conflicting datacontenttype spellings")
		}
		value.DataContentType = decoded.LegacyContentType
	}
	if decoded.LegacyAccountID != "" {
		if value.AccountID != "" && value.AccountID != decoded.LegacyAccountID {
			return errors.New("conflicting accountid spellings")
		}
		value.AccountID = decoded.LegacyAccountID
	}
	*e = Envelope(value)
	return nil
}

const (
	CloudEventsSpecVersion = "1.0"
	JSONDataContentType    = "application/json"
	// EnvelopeStringMax bounds event identity strings in ingress and receipt queries.
	EnvelopeStringMax = 256
)

// Normalize fills server-owned defaults and validates the complete envelope.
// accountID is always authoritative; a caller-supplied account_id must match
// it so a publish can never cross tenant boundaries. Compact 32-hex UUIDs are
// accepted alongside the standard hyphenated spelling used by SQL.
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

	if e.AccountID != "" && !sameAccountID(e.AccountID, accountID) {
		return Envelope{}, fmt.Errorf("account_id must match the authenticated account")
	}
	if _, err := parseAccountUUID(accountID); err != nil {
		return Envelope{}, errors.New("account_id must be a UUID")
	}
	// The authenticated account is authoritative even when the caller omitted
	// account_id. Keep its supplied spelling so API responses remain stable.
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
	if e.ID == "" || len(e.ID) > EnvelopeStringMax {
		return errors.New("id is required and must be at most 256 characters")
	}
	if e.Source == "" || len(e.Source) > EnvelopeStringMax {
		return errors.New("source is required and must be at most 256 characters")
	}
	if _, err := url.Parse(e.Source); err != nil || strings.ContainsAny(e.Source, " \t\r\n\"<>\\^`{|}") {
		return errors.New("source must be a valid URI-reference")
	}
	for _, char := range e.Source {
		if char < 0x21 || char > 0x7e {
			return errors.New("source must be a valid URI-reference")
		}
	}
	if e.Type == "" || len(e.Type) > EnvelopeStringMax {
		return errors.New("type is required and must be at most 256 characters")
	}
	if len(e.SchemaVersion) > 64 {
		return errors.New("schemaversion must be at most 64 characters")
	}
	if e.DataContentType != JSONDataContentType {
		return fmt.Errorf("data_content_type must be %q", JSONDataContentType)
	}
	if _, err := parseAccountUUID(e.AccountID); err != nil {
		return errors.New("account_id must be a UUID")
	}
	if (e.AppID == "") != (e.PlatformTenantID == "") {
		return errors.New("appid and platformtenantid must be provided together")
	}
	if e.AppID != "" {
		if _, err := uuid.Parse(e.AppID); err != nil {
			return errors.New("appid must be a UUID")
		}
		if _, err := uuid.Parse(e.PlatformTenantID); err != nil {
			return errors.New("platformtenantid must be a UUID")
		}
		if e.TenantEventID == "" || len(e.TenantEventID) > EnvelopeStringMax {
			return errors.New("tenanteventid must be provided and at most 256 characters")
		}
	} else if e.TenantEventID != "" {
		return errors.New("tenanteventid requires appid and platformtenantid")
	}
	if len(e.Data) == 0 || !json.Valid(e.Data) {
		return errors.New("data must be valid JSON")
	}
	if e.Time.IsZero() {
		return errors.New("time is required")
	}
	return nil
}

func parseAccountUUID(value string) (uuid.UUID, error) {
	if parsed, err := uuid.Parse(value); err == nil {
		return parsed, nil
	}
	if len(value) == 32 {
		if raw, err := hex.DecodeString(value); err == nil && len(raw) == 16 {
			var parsed uuid.UUID
			copy(parsed[:], raw)
			return parsed, nil
		}
	}
	return uuid.Nil, errors.New("account_id must be a UUID")
}
