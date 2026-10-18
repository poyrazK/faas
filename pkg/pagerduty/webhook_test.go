package pagerduty

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func acknowledgementBody(at time.Time, region string) []byte {
	host := "api.pagerduty.com"
	if region == "eu" {
		host = "api.eu.pagerduty.com"
	}
	body, _ := json.Marshal(map[string]any{"event": map[string]any{"id": "provider-event-1", "event_type": "incident.acknowledged", "resource_type": "incident", "occurred_at": at, "data": map[string]any{"id": "PINC001", "type": "incident", "status": "acknowledged", "incident_key": "gregale:mcp:00000000-0000-0000-0000-000000000001", "self": "https://" + host + "/incidents/PINC001", "service": map[string]string{"id": "PSVC001"}}, "agent": map[string]string{"summary": "PRIVATE_ACTOR_EMAIL"}}})
	return body
}
func TestMCPReadinessPagerDutyWebhookSignatures(t *testing.T) {
	body := acknowledgementBody(time.Now(), "eu")
	secret := strings.Repeat("s", 32)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	sig := "v1=" + hex.EncodeToString(mac.Sum(nil))
	for _, header := range []string{sig, "v2=unknown, " + sig, "v1=" + strings.Repeat("0", 64) + ", " + sig} {
		if !VerifyWebhook(secret, body, header) {
			t.Fatal("valid signature rejected")
		}
	}
	for _, header := range []string{"", "v2=" + strings.TrimPrefix(sig, "v1="), "v1=garbage", "v1=" + strings.Repeat("0", 64), strings.Repeat("a", api.MCPReadinessPagerDutyWebhookHeaderMaxBytes+1), strings.Join(makeSignatures(sig, api.MCPReadinessPagerDutyWebhookMaxSignatures+1), ",")} {
		if VerifyWebhook(secret, body, header) {
			t.Fatal("invalid signature accepted")
		}
	}
	if VerifyWebhook(secret, append(body, ' '), sig) || VerifyWebhook("wrong-secret-123456", body, sig) {
		t.Fatal("signature failed to bind body and key")
	}
	if VerifyWebhook(secret, make([]byte, api.MCPReadinessPagerDutyWebhookBodyMaxBytes+1), sig) {
		t.Fatal("oversized body accepted")
	}
}
func makeSignatures(signature string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = signature
	}
	return out
}
func TestMCPReadinessPagerDutyWebhookParsing(t *testing.T) {
	now := time.Now().UTC()
	for _, region := range []string{"us", "eu"} {
		e, apply, err := ParseAcknowledgement(acknowledgementBody(now, region), now)
		if err != nil || !apply || e.Region != region || e.ServiceID != "PSVC001" || e.IncidentID != "00000000-0000-0000-0000-000000000001" {
			t.Fatal(e, apply, err)
		}
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "PRIVATE_ACTOR") {
			t.Fatal("actor retained")
		}
	}
	for _, body := range []string{`{"event":{"event_type":"incident.resolved"}}`, strings.ReplaceAll(string(acknowledgementBody(now, "us")), "gregale:mcp:00000000-0000-0000-0000-000000000001", "another-product-key")} {
		if _, apply, err := ParseAcknowledgement([]byte(body), now); err != nil || apply {
			t.Fatal("unrelated event not ignored", apply, err)
		}
	}
	base := string(acknowledgementBody(now, "us"))
	invalid := [][]byte{
		[]byte(`{} {}`), []byte(`{"event":{"id":"one","ID":"two"}}`),
		[]byte(strings.ReplaceAll(base, "gregale:mcp:00000000-0000-0000-0000-000000000001", "gregale:mcp:invalid")),
		[]byte(strings.ReplaceAll(base, "https://api.pagerduty.com/incidents/PINC001", "http://api.pagerduty.com/incidents/PINC001")),
		[]byte(strings.ReplaceAll(base, "https://api.pagerduty.com/incidents/PINC001", "https://api.pagerduty.com.evil.example/incidents/PINC001")),
		[]byte(strings.ReplaceAll(base, `"status":"acknowledged"`, `"status":"resolved"`)),
		acknowledgementBody(now.Add(-8*24*time.Hour), "us"), acknowledgementBody(now.Add(6*time.Minute), "us"),
		[]byte(`{"unknown":` + strings.Repeat("[", api.MCPReadinessPagerDutyWebhookJSONMaxDepth+1) + "0" + strings.Repeat("]", api.MCPReadinessPagerDutyWebhookJSONMaxDepth+1) + "}"),
	}
	for n, body := range invalid {
		if _, _, err := ParseAcknowledgement(body, now); err == nil {
			t.Fatal("invalid event accepted", n)
		}
	}
}
