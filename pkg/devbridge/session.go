// Package devbridge implements the identity and transport contracts for local
// processes participating in a Gregale development environment.
package devbridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/credhash"
)

var ErrUnauthorized = errors.New("dev bridge session unauthorized")

// Scope is resolved by apid from owned development resources, never from
// caller-controlled routing headers. Dependencies are explicit app IDs.
type Scope struct {
	AccountID        string   `json:"account_id"`
	DeveloperID      string   `json:"developer_id"`
	EnvironmentID    string   `json:"environment_id"`
	ProjectID        string   `json:"project_id"`
	TargetAppID      string   `json:"target_app_id"`
	DependencyAppIDs []string `json:"dependency_app_ids,omitempty"`
}

// Session persists credential digests only. Attachment and request credentials
// are distinct so a browser cannot attach a replacement laptop connection.
type Session struct {
	ID               string     `json:"id"`
	Scope            Scope      `json:"scope"`
	ExpiresAt        time.Time  `json:"expires_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	AttachmentDigest []byte     `json:"-"`
	RequestDigest    []byte     `json:"-"`
}

// Dev-bridge credentials belong to the dev-bridge credential family (ADR
// 20261029202403 amendment 1): their digests are keyed under its ring,
// which apid and bridged hold; gatewayd-internal asks apid to match them.
const (
	AttachmentDigestColumn credhash.Tag = "dev_bridge_sessions.attachment_digest"
	RequestDigestColumn    credhash.Tag = "dev_bridge_sessions.request_digest"
)

func storedDigest(column credhash.Tag, credential string) []byte {
	sum := sha256.Sum256([]byte(credential))
	return credhash.For(credhash.FamilyDevBridge).Store(column, sum[:])
}

type Credentials struct {
	AttachmentToken string `json:"attachment_token"`
	RequestToken    string `json:"request_token"`
}

func token() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// NewSession is called only after resource authorization. Expiry is supplied
// by the control plane's lease policy rather than chosen by the client.
func NewSession(scope Scope, now, expiry time.Time) (Session, Credentials, error) {
	if scope.AccountID == "" || scope.DeveloperID == "" || scope.EnvironmentID == "" || scope.TargetAppID == "" || !expiry.After(now) {
		return Session{}, Credentials{}, ErrUnauthorized
	}
	id, err := token()
	if err != nil {
		return Session{}, Credentials{}, err
	}
	attach, err := token()
	if err != nil {
		return Session{}, Credentials{}, err
	}
	request, err := token()
	if err != nil {
		return Session{}, Credentials{}, err
	}
	scope.DependencyAppIDs = append([]string(nil), scope.DependencyAppIDs...)
	return Session{ID: id, Scope: scope, ExpiresAt: expiry.UTC(), AttachmentDigest: storedDigest(AttachmentDigestColumn, attach), RequestDigest: storedDigest(RequestDigestColumn, request)}, Credentials{attach, request}, nil
}

func (s Session) valid(now time.Time, credential string, column credhash.Tag, stored []byte) bool {
	if credential == "" || s.RevokedAt != nil || !now.Before(s.ExpiresAt) {
		return false
	}
	actual := sha256.Sum256([]byte(credential))
	return credhash.Match(context.Background(), credhash.FamilyDevBridge, column, actual[:], stored)
}

func (s Session) AuthorizeAttachment(now time.Time, credential string) error {
	if !s.valid(now, credential, AttachmentDigestColumn, s.AttachmentDigest) {
		return ErrUnauthorized
	}
	return nil
}

func (s Session) AuthorizeRequest(now time.Time, credential, accountID, environmentID, appID string) error {
	if accountID != s.Scope.AccountID || environmentID != s.Scope.EnvironmentID || appID != s.Scope.TargetAppID || !s.valid(now, credential, RequestDigestColumn, s.RequestDigest) {
		return ErrUnauthorized
	}
	return nil
}

func (s Session) AuthorizeDependency(now time.Time, credential, accountID, environmentID, appID string) error {
	if accountID != s.Scope.AccountID || environmentID != s.Scope.EnvironmentID || !s.valid(now, credential, AttachmentDigestColumn, s.AttachmentDigest) {
		return ErrUnauthorized
	}
	for _, allowed := range s.Scope.DependencyAppIDs {
		if allowed == appID {
			return nil
		}
	}
	return ErrUnauthorized
}

// AuthorizeContextRoute lets a request participate only in its selected
// application graph. It cannot attach a tunnel or expand the dependency set.
func (s Session) AuthorizeContextRoute(now time.Time, c RequestContext, environmentID, appID string) error {
	if c.SessionID != s.ID || c.AccountID != s.Scope.AccountID || environmentID != s.Scope.EnvironmentID || !s.valid(now, c.Token, RequestDigestColumn, s.RequestDigest) {
		return ErrUnauthorized
	}
	if appID == s.Scope.TargetAppID {
		return nil
	}
	for _, allowed := range s.Scope.DependencyAppIDs {
		if allowed == appID {
			return nil
		}
	}
	return ErrUnauthorized
}
