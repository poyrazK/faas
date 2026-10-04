package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type PutRegionalReleaseGrantRequest struct {
	GlobalAppID string    `json:"global_app_id"`
	TokenSHA256 string    `json:"token_sha256"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type RegionalReleaseGrantResponse struct {
	GrantID       string    `json:"grant_id"`
	CellID        string    `json:"cell_id"`
	RegionalAppID string    `json:"regional_app_id"`
	GlobalAppID   string    `json:"global_app_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	Status        string    `json:"status"`
}

func GenerateRegionalReleaseToken() (string, []byte, error) {
	random := make([]byte, RegionalReleaseTokenRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", nil, fmt.Errorf("api: generate regional release token: %w", err)
	}
	token := RegionalReleaseTokenPrefix + hex.EncodeToString(random)
	return token, HashAPIKey(token), nil
}

func ValidRegionalReleaseTokenFormat(token string) bool {
	if !strings.HasPrefix(token, RegionalReleaseTokenPrefix) {
		return false
	}
	body := strings.TrimPrefix(token, RegionalReleaseTokenPrefix)
	if len(body) != RegionalReleaseTokenRandomBytes*2 || body != strings.ToLower(body) {
		return false
	}
	_, err := hex.DecodeString(body)
	return err == nil
}

func (c *Client) PutRegionalReleaseGrant(ctx context.Context, cellID, appID, grantID string, req PutRegionalReleaseGrantRequest) (RegionalReleaseGrantResponse, error) {
	var out RegionalReleaseGrantResponse
	path, err := regionalReleaseGrantPath(cellID, appID, grantID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "PUT", path, req, &out, "")
}
func (c *Client) GetRegionalReleaseGrant(ctx context.Context, cellID, appID, grantID string) (RegionalReleaseGrantResponse, error) {
	var out RegionalReleaseGrantResponse
	path, err := regionalReleaseGrantPath(cellID, appID, grantID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "GET", path, nil, &out, "")
}
func (c *Client) RevokeRegionalReleaseGrant(ctx context.Context, cellID, appID, grantID string) (RegionalReleaseGrantResponse, error) {
	var out RegionalReleaseGrantResponse
	path, err := regionalReleaseGrantPath(cellID, appID, grantID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "DELETE", path, nil, &out, "")
}
func regionalReleaseGrantPath(cellID, appID, grantID string) (string, error) {
	path, err := cellImageReleasePath(cellID, appID, grantID)
	if err != nil {
		return "", err
	}
	return strings.Replace(path, "/image-releases/", "/release-grants/", 1), nil
}
