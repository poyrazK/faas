package pagerduty

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Acknowledgement contains only allowlisted fields; raw payloads and actors are not retained.
type Acknowledgement struct {
	Action             string
	EventID            string
	IncidentID         string
	ProviderIncidentID string
	ServiceID          string
	Region             string
	OccurredAt         time.Time
}

func VerifyWebhook(secret string, body []byte, header string) bool {
	if !api.ValidMCPReadinessPagerDutySigningSecret(secret) || len(body) > api.MCPReadinessPagerDutyWebhookBodyMaxBytes || len(header) > api.MCPReadinessPagerDutyWebhookHeaderMaxBytes {
		return false
	}
	parts := strings.Split(header, ",")
	if len(parts) > api.MCPReadinessPagerDutyWebhookMaxSignatures {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	valid := false
	for _, part := range parts {
		value, ok := strings.CutPrefix(strings.TrimSpace(part), "v1=")
		if !ok {
			continue
		}
		signature, err := hex.DecodeString(value)
		if err != nil || len(signature) != sha256.Size {
			continue
		}
		match := hmac.Equal(expected, signature)
		valid = match || valid
	}
	return valid
}

// ParseAcknowledgement accepts acknowledgements and optionally signed unacknowledgements.
// Unrelated valid JSON events are ignored, without changing readiness or recording payloads.
func ParseAcknowledgement(body []byte, now time.Time, releaseSync ...bool) (Acknowledgement, bool, error) {
	var payload struct {
		Event struct {
			ID       string    `json:"id"`
			Type     string    `json:"event_type"`
			Resource string    `json:"resource_type"`
			At       time.Time `json:"occurred_at"`
			Data     struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Status  string `json:"status"`
				Key     string `json:"incident_key"`
				Self    string `json:"self"`
				Service struct {
					ID string `json:"id"`
				} `json:"service"`
			} `json:"data"`
		} `json:"event"`
	}
	invalid := errors.New("invalid PagerDuty acknowledgement")
	if len(body) > api.MCPReadinessPagerDutyWebhookBodyMaxBytes || !utf8.Valid(body) || !json.Valid(body) || uniqueWebhookJSON(json.NewDecoder(bytes.NewReader(body)), 0) != nil {
		return Acknowledgement{}, false, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return Acknowledgement{}, false, invalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Acknowledgement{}, false, invalid
	}
	e := payload.Event
	release := e.Type == "incident.unacknowledged" && len(releaseSync) == 1 && releaseSync[0]
	if e.Type != "incident.acknowledged" && !release {
		return Acknowledgement{}, false, nil
	}
	key, ok := strings.CutPrefix(e.Data.Key, "gregale:mcp:")
	if !ok {
		return Acknowledgement{}, false, nil
	}
	status := "acknowledged"
	action := ""
	if release {
		status = "triggered"
		action = "release"
	}
	if uuid.Validate(key) != nil || key != uuid.MustParse(key).String() || e.Resource != "incident" || e.Data.Type != "incident" || e.Data.Status != status || !api.ValidMCPReadinessPagerDutyProviderID(e.ID) || !api.ValidMCPReadinessPagerDutyProviderID(e.Data.ID) || !api.ValidMCPReadinessPagerDutyProviderID(e.Data.Service.ID) || e.At.IsZero() || e.At.After(now.Add(time.Duration(api.MCPReadinessPagerDutyWebhookFutureSkewSeconds)*time.Second)) || now.Sub(e.At) > time.Duration(api.MCPReadinessPagerDutyWebhookMaxAgeSeconds)*time.Second {
		return Acknowledgement{}, false, invalid
	}
	region := "us"
	if e.Data.Self == "https://api.eu.pagerduty.com/incidents/"+e.Data.ID {
		region = "eu"
	} else if e.Data.Self != "https://api.pagerduty.com/incidents/"+e.Data.ID {
		return Acknowledgement{}, false, invalid
	}
	return Acknowledgement{Action: action, EventID: e.ID, IncidentID: key, ProviderIncidentID: e.Data.ID, ServiceID: e.Data.Service.ID, Region: region, OccurredAt: e.At.UTC()}, true, nil
}

func uniqueWebhookJSON(d *json.Decoder, depth int) error {
	if depth > api.MCPReadinessPagerDutyWebhookJSONMaxDepth {
		return errors.New("webhook JSON depth exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			s = strings.ToLower(s)
			if !ok || seen[s] {
				return errors.New("duplicate webhook JSON key")
			}
			seen[s] = true
		}
		if err := uniqueWebhookJSON(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
