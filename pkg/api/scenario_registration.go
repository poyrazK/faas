package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

// ScenarioTestRegistration exposes immutable workload IDs, never credentials.
type ScenarioTestRegistration struct {
	SchemaVersion int               `json:"schema_version"`
	Members       map[string]string `json:"members"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	ExpiryState   string            `json:"expiry_state,omitempty"`
}

func ScenarioTestRegistrationSHA256(members map[string]string) string {
	body, _ := json.Marshal(members)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (c *Client) GetScenarioTestRegistration(ctx context.Context, runID string) (ScenarioTestRegistration, error) {
	var out ScenarioTestRegistration
	err := c.do(ctx, "GET", "/v1/dev/test-runs/"+runID, nil, &out)
	return out, err
}

// The dedicated conditional route is absent on older servers, so a rolling
// upgrade cannot accidentally turn this request into an unconditional delete.
func (c *Client) DeleteScenarioTestRegistration(ctx context.Context, runID, digest string) error {
	current, err := c.GetScenarioTestRegistration(ctx, runID)
	if err != nil {
		return err
	}
	if current.SchemaVersion != 1 || current.Members == nil {
		return errors.New("registration read capability unavailable")
	}
	if len(current.Members) == 0 {
		return nil
	}
	if ScenarioTestRegistrationSHA256(current.Members) != digest {
		return errors.New("registration membership changed")
	}
	return c.do(ctx, "DELETE", "/v1/dev/test-runs/"+runID+"/registration?members_sha256="+url.QueryEscape(digest), nil, nil)
}
