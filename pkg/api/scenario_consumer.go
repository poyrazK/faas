package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

type ScenarioTestConsumerRequest struct {
	AppID     string    `json:"app_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ScenarioTestConsumerReceipt struct {
	Cancelled     bool                 `json:"cancelled,omitempty"`
	SchemaVersion int                  `json:"schema_version"`
	IntentSHA256  string               `json:"intent_sha256"`
	ExpiresAt     time.Time            `json:"expires_at"`
	Consumer      *APIConsumerResponse `json:"consumer"`
}

var scenarioConsumerRun = regexp.MustCompile(`^[0-9a-f]{32}$`)

func ScenarioTestConsumerIntent(run, name string, req ScenarioTestConsumerRequest) (string, error) {
	if !scenarioConsumerRun.MatchString(run) || !ValidAppSlug(name) || req.AppID == "" || req.ExpiresAt.IsZero() {
		return "", fmt.Errorf("invalid test consumer intent")
	}
	req.ExpiresAt = req.ExpiresAt.UTC().Truncate(time.Microsecond)
	body, err := json.Marshal(struct {
		Run, Name string
		Request   ScenarioTestConsumerRequest
	}{run, name, req})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (c *Client) EnsureScenarioTestConsumer(ctx context.Context, run, name string, req ScenarioTestConsumerRequest) (ScenarioTestConsumerReceipt, error) {
	var out ScenarioTestConsumerReceipt
	intent, err := ScenarioTestConsumerIntent(run, name, req)
	if err != nil {
		return out, err
	}
	err = c.doWithIdempotencyKey(ctx, "PUT", "/v1/dev/test-runs/"+run+"/consumers/"+url.PathEscape(name), req, &out, intent)
	return out, err
}

func (c *Client) LookupScenarioTestConsumer(ctx context.Context, run, name string, req ScenarioTestConsumerRequest) (ScenarioTestConsumerReceipt, error) {
	var out ScenarioTestConsumerReceipt
	if _, err := ScenarioTestConsumerIntent(run, name, req); err != nil {
		return out, err
	}
	query := url.Values{"app_id": {req.AppID}, "expires_at": {req.ExpiresAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)}}
	err := c.do(ctx, "GET", "/v1/dev/test-runs/"+run+"/consumers/"+url.PathEscape(name)+"?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) RevokeScenarioTestConsumer(ctx context.Context, run, name string, req ScenarioTestConsumerRequest, consumerID string) (ScenarioTestConsumerReceipt, error) {
	var out ScenarioTestConsumerReceipt
	intent, err := ScenarioTestConsumerIntent(run, name, req)
	if err != nil {
		return out, err
	}
	query := url.Values{"app_id": {req.AppID}, "expires_at": {req.ExpiresAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)}, "consumer_id": {consumerID}}
	err = c.doWithIdempotencyKey(ctx, "DELETE", "/v1/dev/test-runs/"+run+"/consumers/"+url.PathEscape(name)+"?"+query.Encode(), nil, &out, intent)
	return out, err
}

// CancelScenarioTestConsumer atomically fences creation and revokes any committed consumer.
func (c *Client) CancelScenarioTestConsumer(ctx context.Context, run, name string, req ScenarioTestConsumerRequest) (ScenarioTestConsumerReceipt, error) {
	var out ScenarioTestConsumerReceipt
	intent, err := ScenarioTestConsumerIntent(run, name, req)
	if err != nil {
		return out, err
	}
	query := url.Values{"app_id": {req.AppID}, "expires_at": {req.ExpiresAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)}}
	err = c.doWithIdempotencyKey(ctx, "DELETE", "/v1/dev/test-runs/"+run+"/consumers/"+url.PathEscape(name)+"?"+query.Encode(), nil, &out, intent)
	return out, err
}
