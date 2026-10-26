package oci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Amazon ECR authenticates registry requests with a 12-hour password from
// its GetAuthorizationToken API (ADR-20261025124445). A credential stored for
// an ECR registry whose username is an IAM access key ID is that key: the
// client signs GetAuthorizationToken with it and pulls with the token it
// returns. The secret key is never sent to the registry.

var (
	ecrRegistryHost = regexp.MustCompile(`^([0-9]{12})\.dkr\.ecr(-fips)?\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)
	awsAccessKeyID  = regexp.MustCompile(`^AKIA[A-Z0-9]{16}$`)
)

const (
	ecrGetAuthorizationTokenTarget = "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken"
	// ecrTokenRenewBefore is how long before its expiry an exchanged token
	// is replaced, so a pull never starts with a token about to lapse.
	ecrTokenRenewBefore = 30 * time.Minute
)

type ecrToken struct {
	auth    *BasicAuth
	renewAt time.Time
}

// registryAuth turns a stored credential into the one the registry accepts.
// Only an IAM access key for an ECR registry changes; every other credential
// is returned as it is.
func (c *RegistryClient) registryAuth(ctx context.Context, r Reference, auth *BasicAuth) (*BasicAuth, error) {
	if auth == nil || !awsAccessKeyID.MatchString(auth.Username) {
		return auth, nil
	}
	m := ecrRegistryHost.FindStringSubmatch(r.Registry)
	if m == nil {
		return auth, nil
	}
	sum := sha256.Sum256([]byte(r.Registry + "\x00" + auth.Username + "\x00" + auth.Password))
	key := hex.EncodeToString(sum[:])
	if v, ok := c.ecrTokens.Load(key); ok {
		if tok := v.(ecrToken); time.Now().Before(tok.renewAt) {
			return tok.auth, nil
		}
	}
	tok, err := c.exchangeECRKey(ctx, m[1], m[2], m[3], m[4], auth)
	if err != nil {
		return nil, scrubAuthFromError(err, auth)
	}
	c.ecrTokens.Store(key, tok)
	return tok.auth, nil
}

func (c *RegistryClient) exchangeECRKey(ctx context.Context, registryID, fips, region, partitionSuffix string, auth *BasicAuth) (ecrToken, error) {
	endpoint := c.ecrAPIBase
	if endpoint == "" {
		endpoint = "https://api.ecr" + fips + "." + region + ".amazonaws.com" + partitionSuffix
	}
	body := []byte(`{"registryIds":["` + registryID + `"]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/", bytes.NewReader(body))
	if err != nil {
		return ecrToken{}, fmt.Errorf("oci: build ECR token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", ecrGetAuthorizationTokenTarget)
	req.Header.Set("User-Agent", c.ua)
	payload := sha256.Sum256(body)
	creds := aws.Credentials{AccessKeyID: auth.Username, SecretAccessKey: auth.Password}
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(payload[:]), "ecr", region, time.Now()); err != nil {
		return ecrToken{}, fmt.Errorf("oci: sign ECR token request: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return ecrToken{}, fmt.Errorf("oci: ECR token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ecrToken{}, fmt.Errorf("oci: read ECR token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		return ecrToken{}, fmt.Errorf("oci: ECR GetAuthorizationToken returned %d: %s %s", resp.StatusCode, apiErr.Type, apiErr.Message)
	}
	var out struct {
		AuthorizationData []struct {
			AuthorizationToken string  `json:"authorizationToken"`
			ExpiresAt          float64 `json:"expiresAt"`
		} `json:"authorizationData"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.AuthorizationData) == 0 {
		return ecrToken{}, errors.New("oci: ECR GetAuthorizationToken returned no token")
	}
	data := out.AuthorizationData[0]
	decoded, err := base64.StdEncoding.DecodeString(data.AuthorizationToken)
	if err != nil {
		return ecrToken{}, errors.New("oci: ECR returned a malformed token")
	}
	user, password, ok := strings.Cut(string(decoded), ":")
	if !ok || user == "" || password == "" {
		return ecrToken{}, errors.New("oci: ECR returned a malformed token")
	}
	renewAt := time.Unix(int64(data.ExpiresAt), 0).Add(-ecrTokenRenewBefore)
	return ecrToken{auth: &BasicAuth{Username: user, Password: password}, renewAt: renewAt}, nil
}
