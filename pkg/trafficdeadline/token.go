// Package trafficdeadline authenticates an absolute deadline across a managed
// HTTP chain. A token is scoped to the app that receives it, not its next hop.
// adr: 570
package trafficdeadline

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const Header = "X-Gregale-Request-Deadline"

var (
	ErrUnavailable = errors.New("traffic deadline key unavailable")
	ErrInvalid     = errors.New("invalid traffic deadline token")
	ErrExpired     = errors.New("traffic deadline expired")
	ErrClock       = errors.New("traffic deadline clock unavailable")
)

// Claims are authenticated before any deadline or identity is trusted.
type Claims struct {
	AppID      string `json:"a"`
	AccountID  string `json:"t"`
	ChainID    string `json:"c"`
	IssuedNS   int64  `json:"i"`
	DeadlineNS int64  `json:"d"`
}

func (c Claims) Deadline() time.Time { return time.Unix(0, c.DeadlineNS) }
func (c Claims) Issued() time.Time   { return time.Unix(0, c.IssuedNS) }

// Signer is immutable. Its shared key is independent of the session-cookie
// protocol despite using the same operator-provisioned master secret.
type Signer struct {
	key   []byte
	keyID string
	now   func() time.Time
}

func New(master []byte, now func() time.Time) (*Signer, error) {
	if len(master) != sha256.Size {
		return nil, ErrUnavailable
	}
	derive := hmac.New(sha256.New, master)
	_, _ = derive.Write([]byte("gregale/traffic-deadline/v1"))
	key := derive.Sum(nil)
	id := sha256.Sum256(key)
	if now == nil {
		now = time.Now
	}
	return &Signer{key: key, keyID: hex.EncodeToString(id[:8]), now: now}, nil
}

func (s *Signer) Mint(appID, accountID, chainID string, deadline time.Time) (string, error) {
	if s == nil {
		return "", ErrUnavailable
	}
	c := Claims{AppID: appID, AccountID: accountID, ChainID: chainID, IssuedNS: s.now().UnixNano(), DeadlineNS: deadline.UnixNano()}
	if err := c.validate(); err != nil {
		return "", err
	}
	if c.DeadlineNS <= c.IssuedNS {
		return "", ErrExpired
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", ErrInvalid
	}
	body := "v1." + s.keyID + "." + base64.RawURLEncoding.EncodeToString(data)
	token := body + "." + base64.RawURLEncoding.EncodeToString(s.mac(body))
	if len(token) > api.MaxTrafficDeadlineTokenBytes {
		return "", ErrInvalid
	}
	return token, nil
}

func (s *Signer) Verify(token, receivingAppID string) (Claims, error) {
	c, err := s.Authenticate(token)
	if err != nil {
		return Claims{}, err
	}
	if receivingAppID == "" || c.AppID != receivingAppID {
		return Claims{}, ErrInvalid
	}
	return c, nil
}

// Authenticate verifies the MAC and time bounds before source-identity lookup.
// It does not establish which app sent the request or authorize any service.
// The caller must check AppID against resolved instance identity before wake.
func (s *Signer) Authenticate(token string) (Claims, error) {
	if s == nil {
		return Claims{}, ErrUnavailable
	}
	if len(token) == 0 || len(token) > api.MaxTrafficDeadlineTokenBytes {
		return Claims{}, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return Claims{}, ErrInvalid
	}
	if parts[1] != s.keyID {
		return Claims{}, ErrUnavailable
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || !hmac.Equal(signature, s.mac(strings.Join(parts[:3], "."))) {
		return Claims{}, ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var c Claims
	if err := json.Unmarshal(data, &c); err != nil {
		return Claims{}, ErrInvalid
	}
	if err := c.validate(); err != nil {
		return Claims{}, err
	}
	now := s.now().UnixNano()
	if c.IssuedNS > now {
		return Claims{}, ErrClock
	}
	if c.DeadlineNS <= now {
		return Claims{}, ErrExpired
	}
	return c, nil
}

func (s *Signer) mac(body string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(body))
	return mac.Sum(nil)
}

func (c Claims) validate() error {
	chain, err := uuid.Parse(c.ChainID)
	if err != nil || chain == uuid.Nil || c.AppID == "" || c.AccountID == "" || c.IssuedNS <= 0 || c.DeadlineNS <= 0 {
		return ErrInvalid
	}
	if c.DeadlineNS > c.IssuedNS && time.Duration(c.DeadlineNS-c.IssuedNS) > time.Duration(api.MaxServiceReliabilityTimeoutMS)*time.Millisecond {
		return ErrInvalid
	}
	return nil
}
