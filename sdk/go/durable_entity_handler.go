// ADR-934: pure, versioned durable entity guest transitions.
package faas

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// DurableEntityIdentity is verified by Gregale when it invokes the guest.
// Parsing an envelope is not authentication for a public HTTP endpoint.
type DurableEntityIdentity struct {
	AccountID     string `json:"account_id"`
	AppID         string `json:"app_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
	TenantID      string `json:"tenant_id,omitempty"`
	Namespace     string `json:"namespace"`
	Key           string `json:"key"`
}

type DurableEntityState struct {
	Data    json.RawMessage `json:"data"`
	Version uint64          `json:"version"`
	AlarmAt *time.Time      `json:"alarm_at,omitempty"`
}

// DurableEntityHandlerLimits are advertised by the v2 platform, whose engine
// also enforces pending-queue, snapshot and retained-storage limits at commit.
type DurableEntityHandlerLimits struct {
	TransitionBytes    int `json:"transition_bytes"`
	IdentityBytes      int `json:"identity_bytes"`
	OutboxMessages     int `json:"outbox_messages"`
	OutboxPayloadBytes int `json:"outbox_payload_bytes"`
	OutboxBytes        int `json:"outbox_bytes"`
}

type DurableEntityHandlerRequest struct {
	ProtocolVersion int                         `json:"protocol_version"`
	Event           string                      `json:"event,omitempty"`
	Entity          DurableEntityIdentity       `json:"entity"`
	RequestID       string                      `json:"request_id"`
	Payload         json.RawMessage             `json:"payload"`
	State           DurableEntityState          `json:"state"`
	DeploymentID    string                      `json:"deployment_id"`
	Limits          *DurableEntityHandlerLimits `json:"limits,omitempty"`
}

// DurableEntityWebhookIntent describes work; constructing it performs no I/O.
type DurableEntityWebhookIntent struct {
	WebhookID string          `json:"webhook_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// DurableEntityTransition replaces data and alarm state. Nil AlarmAt clears the
// alarm. Omitting Outbox preserves pending messages. Callbacks must be pure:
// sending or writing externally here can escape a rejected state publication.
type DurableEntityTransition struct {
	Data    json.RawMessage              `json:"data"`
	Result  json.RawMessage              `json:"result"`
	AlarmAt *time.Time                   `json:"alarm_at,omitempty"`
	Outbox  []DurableEntityWebhookIntent `json:"outbox,omitempty"`
}

var ErrDurableEntityHandler = errors.New("faas: invalid or unsupported durable entity handler contract")

// DecodeDurableEntityHandlerRequest accepts the v1/v2 guest envelope. Bound the
// HTTP body before calling it. It never exposes bucket or ownership authority.
func DecodeDurableEntityHandlerRequest(body []byte) (DurableEntityHandlerRequest, error) {
	var request DurableEntityHandlerRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var extra any
	if len(body) > DurableEntityHandlerMaxRequestBytes || !utf8.Valid(body) || dec.Decode(&request) != nil || dec.Decode(&extra) != io.EOF || request.validate() != nil {
		return DurableEntityHandlerRequest{}, ErrDurableEntityHandler
	}
	return request, nil
}

func (r DurableEntityHandlerRequest) validate() error {
	if r.ProtocolVersion != DurableEntityHandlerProtocolVersion && r.ProtocolVersion != DurableEntityOutboxHandlerProtocolVersion || r.Event != "" && r.Event != "invoke" && r.Event != "alarm" ||
		!json.Valid(r.Payload) || !json.Valid(r.State.Data) || !durableEntityAlarm(r.State.AlarmAt) {
		return ErrDurableEntityHandler
	}
	maximum := 0 // v1 does not advertise limits; its platform enforces them.
	if r.ProtocolVersion == DurableEntityOutboxHandlerProtocolVersion {
		limits := r.Limits
		if limits == nil || limits.TransitionBytes <= 0 || limits.TransitionBytes > DurableEntityHandlerMaxTransitionBytes || limits.IdentityBytes <= 0 || limits.OutboxMessages <= 0 ||
			limits.OutboxPayloadBytes <= 0 || limits.OutboxBytes < limits.OutboxPayloadBytes || limits.TransitionBytes < limits.OutboxBytes {
			return ErrDurableEntityHandler
		}
		maximum = limits.IdentityBytes
	} else if r.Limits != nil {
		return ErrDurableEntityHandler
	}
	for _, identity := range []string{r.Entity.AccountID, r.Entity.AppID, r.Entity.Namespace, r.Entity.Key, r.RequestID, r.DeploymentID} {
		if !durableEntityIdentity(identity, maximum) {
			return ErrDurableEntityHandler
		}
	}
	for _, identity := range []string{r.Entity.EnvironmentID, r.Entity.TenantID} {
		if identity != "" && !durableEntityIdentity(identity, maximum) {
			return ErrDurableEntityHandler
		}
	}
	return nil
}

// WebhookIntent requires negotiated v2 support and an existing registered app
// webhook UUID. Registration/ownership are validated by the platform at commit
// and acceptance; the SDK never accepts a target URL or signing credential.
func (r DurableEntityHandlerRequest) WebhookIntent(webhookID, eventType string, payload any) (DurableEntityWebhookIntent, error) {
	if r.validate() != nil || r.ProtocolVersion != DurableEntityOutboxHandlerProtocolVersion {
		return DurableEntityWebhookIntent{}, ErrDurableEntityHandler
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return DurableEntityWebhookIntent{}, ErrDurableEntityHandler
	}
	intent := DurableEntityWebhookIntent{WebhookID: webhookID, EventType: eventType, Payload: body}
	if !r.validIntent(intent) {
		return DurableEntityWebhookIntent{}, ErrDurableEntityHandler
	}
	return intent, nil
}

func (r DurableEntityHandlerRequest) validIntent(intent DurableEntityWebhookIntent) bool {
	if !durableEntityWebhookID(intent.WebhookID) || !durableEntityIdentity(intent.EventType, r.Limits.IdentityBytes) || !json.Valid(intent.Payload) {
		return false
	}
	body, err := json.Marshal(intent.Payload) // Includes platform HTML escaping.
	return err == nil && len(intent.Payload) <= r.Limits.OutboxPayloadBytes && len(body) <= r.Limits.OutboxPayloadBytes
}

// EncodeTransition returns response bytes only; it does not commit or deliver.
// HTTP success at the guest is not proof of a successful entity publication.
func (r DurableEntityHandlerRequest) EncodeTransition(next DurableEntityTransition) ([]byte, error) {
	if r.validate() != nil || !json.Valid(next.Data) || !json.Valid(next.Result) || !durableEntityAlarm(next.AlarmAt) ||
		r.ProtocolVersion == DurableEntityHandlerProtocolVersion && next.Outbox != nil {
		return nil, ErrDurableEntityHandler
	}
	if r.ProtocolVersion == DurableEntityOutboxHandlerProtocolVersion {
		if len(next.Outbox) > r.Limits.OutboxMessages {
			return nil, ErrDurableEntityHandler
		}
		for _, intent := range next.Outbox {
			if !r.validIntent(intent) {
				return nil, ErrDurableEntityHandler
			}
		}
		batch, err := json.Marshal(next.Outbox)
		if err != nil || len(batch) > r.Limits.OutboxBytes {
			return nil, ErrDurableEntityHandler
		}
	}
	body, err := json.Marshal(next)
	if err != nil || len(body) > DurableEntityHandlerMaxTransitionBytes || r.Limits != nil && len(body) > r.Limits.TransitionBytes {
		return nil, ErrDurableEntityHandler
	}
	return body, nil
}

func durableEntityIdentity(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && (maximum == 0 || len(value) <= maximum) &&
		strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func durableEntityWebhookID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func durableEntityAlarm(value *time.Time) bool {
	return value == nil || !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}
