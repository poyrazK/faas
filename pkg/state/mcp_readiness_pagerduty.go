package state

import (
	"context"
	"encoding/hex"
	"github.com/onebox-faas/faas/pkg/api"
	"math"
)

type MCPReadinessPagerDutyConfig struct {
	Version              int64
	Enabled              bool
	Region               string
	IntegrationKeySealed []byte
	Fingerprint          string
}
type MCPReadinessPagerDutyStore interface {
	GetMCPReadinessPagerDuty(context.Context, string, string, string) (api.MCPReadinessPagerDutyPolicy, error)
	SetMCPReadinessPagerDuty(context.Context, string, string, string, int64, MCPReadinessPagerDutyConfig) (api.MCPReadinessPagerDutyPolicy, error)
}

func readinessPagerDutyView(c MCPReadinessPagerDutyConfig) api.MCPReadinessPagerDutyPolicy {
	out := api.MCPReadinessPagerDutyPolicy{Version: c.Version, Enabled: c.Enabled, Region: readinessPagerDutyRegion(c)}
	if c.Enabled {
		out.IntegrationKeyMasked = "***"
	}
	return out
}
func readinessPagerDutyRegion(c MCPReadinessPagerDutyConfig) string {
	if c.Region == "" {
		return "us"
	}
	return c.Region
}
func validateReadinessPagerDutyConfig(expected int64, c MCPReadinessPagerDutyConfig) error {
	if _, ok := api.MCPReadinessPagerDutyEndpointForRegion(c.Region); !ok || !c.Enabled && readinessPagerDutyRegion(c) != "us" {
		return ErrInvalidArgument
	}
	if expected < 0 || expected == math.MaxInt64 || len(c.IntegrationKeySealed) > api.MCPReadinessPagerDutyCiphertextMaxBytes {
		return ErrInvalidArgument
	}
	if !c.Enabled {
		if len(c.IntegrationKeySealed) != 0 || c.Fingerprint != "" {
			return ErrInvalidArgument
		}
		return nil
	}
	b, e := hex.DecodeString(c.Fingerprint)
	if e != nil || len(b) != 32 || hex.EncodeToString(b) != c.Fingerprint || len(c.IntegrationKeySealed) == 0 {
		return ErrInvalidArgument
	}
	return nil
}
func (m *MemStore) GetMCPReadinessPagerDuty(_ context.Context, account, app, id string) (api.MCPReadinessPagerDutyPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessPagerDutyPolicy{}, ErrNotFound
	}
	rule, ok := m.alertRules[id]
	if !ok {
		return api.MCPReadinessPagerDutyPolicy{}, ErrNotFound
	}
	if err := readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	return readinessPagerDutyView(m.readinessPagerDutyPolicies[id]), nil
}
func (m *MemStore) SetMCPReadinessPagerDuty(_ context.Context, account, app, id string, expected int64, policy MCPReadinessPagerDutyConfig) (api.MCPReadinessPagerDutyPolicy, error) {
	if validateReadinessPagerDutyConfig(expected, policy) != nil {
		return api.MCPReadinessPagerDutyPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessPagerDutyPolicy{}, ErrNotFound
	}
	rule, ok := m.alertRules[id]
	if !ok {
		return api.MCPReadinessPagerDutyPolicy{}, ErrNotFound
	}
	if err := readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessPagerDutyPolicy{}, err
	}
	if m.readinessPagerDutyPolicies[id].Version != expected {
		return api.MCPReadinessPagerDutyPolicy{}, ErrConflict
	}
	if sameReadinessPagerDuty(policy, m.readinessFallbackPolicies[id]) {
		return api.MCPReadinessPagerDutyPolicy{}, ErrInvalidArgument
	}
	out := policy
	out.Region = readinessPagerDutyRegion(policy)
	out.Version = expected + 1
	out.IntegrationKeySealed = append([]byte{}, out.IntegrationKeySealed...)
	m.readinessPagerDutyPolicies[id] = out
	return readinessPagerDutyView(out), nil
}

func (m *MemStore) readinessDeliveryPolicyLocked(id string) MCPReadinessFallbackConfig {
	p := m.readinessFallbackPolicies[id]
	p.PrimaryPagerDuty = m.readinessPagerDutyPolicies[id]
	return p
}
func readinessProvider(d readinessDestination) string {
	if d.Provider == "pagerduty" {
		return "pagerduty"
	}
	return "webhook"
}
func readinessReceiverIdentity(d readinessDestination) string {
	if d.Provider == "pagerduty" {
		return "pagerduty:" + d.URL + ":" + d.ReceiverID
	}
	return d.URL
}
