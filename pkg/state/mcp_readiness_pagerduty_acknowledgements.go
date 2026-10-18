package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// The current binding is deliberately revocable; old signing keys are never retained.
type MCPReadinessPagerDutyAcknowledgementConfig struct {
	SyncUnacknowledgements   bool
	LegacyReceiver           bool
	Destination              string
	ExpectedFallbackVersion  int64
	FallbackVersion          int64
	Version                  int64
	Enabled                  bool
	ExpectedPagerDutyVersion int64
	ServiceID                string
	SigningSecretSealed      []byte
	PagerDutyVersion         int64
	Region                   string
	Fingerprint              string
	AccountID                string
	AppID                    string
}
type MCPReadinessPagerDutyAcknowledgementInput struct {
	BindingDestination                                         string `json:"-"`
	RequireLegacyReceiver                                      bool   `json:"-"`
	EventID, IncidentID, ProviderIncidentID, ServiceID, Region string
	OccurredAt                                                 time.Time
	Action                                                     string `json:",omitempty"`
}
type readinessPagerDutyAcknowledgementRecord struct {
	RuleID  string
	Input   MCPReadinessPagerDutyAcknowledgementInput
	Receipt api.MCPReadinessPagerDutyAcknowledgementEvent
}
type MCPReadinessPagerDutyAcknowledgementStore interface {
	GetMCPReadinessPagerDutyAcknowledgements(context.Context, string, string, string, ...string) (api.MCPReadinessPagerDutyAcknowledgementPolicy, error)
	SetMCPReadinessPagerDutyAcknowledgements(context.Context, string, string, string, int64, MCPReadinessPagerDutyAcknowledgementConfig) (api.MCPReadinessPagerDutyAcknowledgementPolicy, error)
	ListMCPReadinessPagerDutyAcknowledgementEvents(context.Context, string, string, string, string, int) (api.MCPReadinessPagerDutyAcknowledgementEvents, error)
	ReadMCPReadinessPagerDutyAcknowledgementReceiver(context.Context, string, ...string) (MCPReadinessPagerDutyAcknowledgementConfig, error)
	ApplyMCPReadinessPagerDutyAcknowledgement(context.Context, string, int64, MCPReadinessPagerDutyAcknowledgementInput, time.Time) (api.MCPReadinessPagerDutyAcknowledgementEvent, error)
}

func readinessPagerDutyAcknowledgementView(rule string, c MCPReadinessPagerDutyAcknowledgementConfig) api.MCPReadinessPagerDutyAcknowledgementPolicy {
	out := api.MCPReadinessPagerDutyAcknowledgementPolicy{Version: c.Version, Enabled: c.Enabled, WebhookPath: "/v1/webhooks/mcp-readiness/pagerduty/" + rule}
	out.SyncUnacknowledgements = c.SyncUnacknowledgements
	out.Destination = readinessPagerDutyAcknowledgementDestination(c)
	if !c.LegacyReceiver {
		out.WebhookPath += "/" + out.Destination
	}
	if c.Enabled {
		out.Destination = readinessPagerDutyAcknowledgementDestination(c)
		out.FallbackVersion = c.FallbackVersion
		out.PagerDutyVersion = c.PagerDutyVersion
		out.Region = c.Region
		out.ServiceID = c.ServiceID
		out.SigningSecretMasked = "***"
	}
	return out
}
func bindReadinessPagerDutyAcknowledgements(expected int64, c MCPReadinessPagerDutyAcknowledgementConfig, p MCPReadinessFallbackConfig) (MCPReadinessPagerDutyAcknowledgementConfig, error) {
	if (c.Destination != "" && c.Destination != "primary" && c.Destination != "fallback") || expected < 0 || expected == math.MaxInt64 || len(c.SigningSecretSealed) > api.MCPReadinessPagerDutyCiphertextMaxBytes {
		return c, ErrInvalidArgument
	}
	if c.Enabled {
		if len(c.SigningSecretSealed) == 0 || !api.ValidMCPReadinessPagerDutyProviderID(c.ServiceID) {
			return c, ErrInvalidArgument
		}
		c.Destination = readinessPagerDutyAcknowledgementDestination(c)
		if c.Destination == "fallback" {
			if c.ExpectedFallbackVersion < 1 || c.ExpectedPagerDutyVersion != 0 {
				return c, ErrInvalidArgument
			}
			if !p.Enabled || p.Provider != "pagerduty" || p.Version != c.ExpectedFallbackVersion {
				return c, ErrConflict
			}
			c.FallbackVersion, c.PagerDutyVersion = p.Version, 0
			c.Region, c.Fingerprint = readinessPagerDutyRegion(fallbackPagerDuty(p)), p.Fingerprint
		} else {
			if c.Destination != "primary" || c.ExpectedPagerDutyVersion < 1 || c.ExpectedFallbackVersion != 0 {
				return c, ErrInvalidArgument
			}
			primary := p.PrimaryPagerDuty
			if !primary.Enabled || primary.Version != c.ExpectedPagerDutyVersion {
				return c, ErrConflict
			}
			c.PagerDutyVersion, c.FallbackVersion = primary.Version, 0
			c.Region, c.Fingerprint = readinessPagerDutyRegion(primary), primary.Fingerprint
		}
	} else {
		if c.SyncUnacknowledgements || c.ExpectedFallbackVersion != 0 || len(c.SigningSecretSealed) != 0 || c.ServiceID != "" || c.ExpectedPagerDutyVersion != 0 {
			return c, ErrInvalidArgument
		}
		c = MCPReadinessPagerDutyAcknowledgementConfig{Destination: c.Destination, LegacyReceiver: c.LegacyReceiver}
	}
	c.Version = expected + 1
	c.SigningSecretSealed = append([]byte{}, c.SigningSecretSealed...)
	return c, nil
}
func validateReadinessPagerDutyAcknowledgement(e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) error {
	if (e.Action != "" && e.Action != "release") || uuid.Validate(e.IncidentID) != nil || !api.ValidMCPReadinessPagerDutyProviderID(e.EventID) || !api.ValidMCPReadinessPagerDutyProviderID(e.ProviderIncidentID) || !api.ValidMCPReadinessPagerDutyProviderID(e.ServiceID) || (e.Region != "us" && e.Region != "eu") || e.OccurredAt.IsZero() || now.IsZero() || e.OccurredAt.After(now.Add(time.Duration(api.MCPReadinessPagerDutyWebhookFutureSkewSeconds)*time.Second)) || now.Sub(e.OccurredAt) > time.Duration(api.MCPReadinessPagerDutyWebhookMaxAgeSeconds)*time.Second {
		return ErrInvalidArgument
	}
	return nil
}
func readinessPagerDutyAcknowledgementDigest(e MCPReadinessPagerDutyAcknowledgementInput) string {
	e.OccurredAt = e.OccurredAt.UTC()
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func readinessPagerDutyAcknowledgementDestination(c MCPReadinessPagerDutyAcknowledgementConfig) string {
	if c.Destination == "" {
		return "primary"
	}
	return c.Destination
}
func readinessPagerDutyAcknowledgementMatches(c MCPReadinessPagerDutyAcknowledgementConfig, i readinessIncident, e MCPReadinessPagerDutyAcknowledgementInput) bool {
	endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(c.Region)
	target := i.Routing.Primary
	if readinessPagerDutyAcknowledgementDestination(c) == "fallback" {
		if !i.Routing.Activated {
			return false
		}
		target = i.Routing.Fallback
	}
	return c.Enabled && c.AccountID == i.AccountID && c.AppID == i.AppID && e.ServiceID == c.ServiceID && e.Region == c.Region && i.Routing.Initialized && target.Provider == "pagerduty" && target.URL == endpoint && target.ReceiverID == c.Fingerprint
}
func decideReadinessPagerDutyLegacyAcknowledgement(i readinessIncident, r readinessAttentionRecord, c MCPReadinessPagerDutyAcknowledgementConfig, e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) (readinessAttentionRecord, api.MCPReadinessPagerDutyAcknowledgementEvent, *readinessAttentionActionRecord, error) {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale:pagerduty-ack:"+i.RuleID+":"+e.EventID)).String()
	receipt := api.MCPReadinessPagerDutyAcknowledgementEvent{Action: "acknowledge", Destination: readinessPagerDutyAcknowledgementDestination(c), Region: c.Region, ServiceID: c.ServiceID, ID: id, EventID: e.EventID, IncidentID: i.ID, ProviderIncidentID: e.ProviderIncidentID, OccurredAt: e.OccurredAt.UTC(), ReceivedAt: now, PolicyVersion: c.Version, AttentionVersion: r.Version, Outcome: "stale"}
	if i.ResolvedAt != nil {
		receipt.Outcome = "resolved"
		return r, receipt, nil, nil
	}
	if e.OccurredAt.Before(i.OpenedAt) || r.LastAction != nil && !e.OccurredAt.After(r.LastAction.CreatedAt) {
		return r, receipt, nil, nil
	}
	if r.Acknowledgement != nil && (readinessAcknowledged(r, now) || r.Acknowledgement.ExpiresAt != nil && !e.OccurredAt.After(*r.Acknowledgement.ExpiresAt)) {
		receipt.Outcome = "already_acknowledged"
		return r, receipt, nil, nil
	}
	request := api.MCPReadinessAttentionAction{RequestID: id, ExpectedVersion: &r.Version, Action: "acknowledge", Reason: "Acknowledged in PagerDuty"}
	updated, attention, err := decideReadinessAttentionAction(i, r, request, now)
	if err != nil {
		return r, receipt, nil, err
	}
	updated.PagerDutyStates, updated.PagerDutyTracking, updated.LocalDecisionAt = r.PagerDutyStates, r.PagerDutyTracking, r.LocalDecisionAt
	attention.Source = "pagerduty"
	attention.ProviderEventID = e.EventID
	attention.ProviderIncidentID = e.ProviderIncidentID
	attention.ProviderOccurredAt = &receipt.OccurredAt
	updated.LastAction = &attention
	updated.Acknowledgement.Source = "pagerduty"
	updated.Acknowledgement.ProviderEventID = e.EventID
	updated.Acknowledgement.ProviderIncidentID = e.ProviderIncidentID
	updated.Acknowledgement.ProviderOccurredAt = &receipt.OccurredAt
	receipt.Outcome = "acknowledged"
	receipt.AttentionVersion = updated.Version
	return updated, receipt, &readinessAttentionActionRecord{AccountID: i.AccountID, AppID: i.AppID, IncidentID: i.ID, Request: request, Receipt: attention}, nil
}

// Empty selects the original callback binding, retained across upgrades.
func readinessPagerDutyAcknowledgementSelector(destinations []string) (string, error) {
	if len(destinations) > 1 {
		return "", ErrInvalidArgument
	}
	if len(destinations) == 0 {
		return "", nil
	}
	d := destinations[0]
	if d != "" && d != "primary" && d != "fallback" {
		return "", ErrInvalidArgument
	}
	return d, nil
}

func readinessPagerDutyAcknowledgementDelivery(e MCPReadinessPagerDutyAcknowledgementInput, c MCPReadinessPagerDutyAcknowledgementConfig) string {
	if e.Action == "release" {
		return readinessPagerDutyAcknowledgementDestination(c)
	}
	return ""
}
