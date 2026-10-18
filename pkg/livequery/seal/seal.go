// Package seal implements sealed live queries (ADR-20261016151947): a customer backend
// seals a query, its parameters and its database with the per-database live
// secret, and realtimed opens the seal before it subscribes a browser to the
// query. A seal is a short-lived bearer capability: whoever holds it may
// subscribe to exactly that query until it expires.
//
// A secret is "glsk1_<kid>_<key>": a 16-character key ID and 32 bytes of key
// material in unpadded base64url. A seal is "gls1.<kid>.<sealed>", where sealed
// is a 12-byte nonce followed by the AES-256-GCM encryption of the query's
// JSON, with "gls1.<kid>" as additional data. The AES key is
// HKDF-SHA256(key material, info "gregale live seal v1"). Every step is
// available in Web Crypto, so the SDK seals in edge runtimes too.
//
// The format carries no session settings. Settings that RLS policies read
// would need per-plan-group isolation inside the round's single snapshot
// transaction; RLS mode covers per-user policies until then.
package seal

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	tokenPrefix  = "gls1"
	secretPrefix = "glsk1"
	kidLength    = 16
	keyLength    = 32
	nonceLength  = 12
	hkdfInfo     = "gregale live seal v1"
)

// Errors that Open and Seal return. Code maps them to live.error codes.
var (
	ErrMalformed  = errors.New("seal: malformed")
	ErrUnknownKey = errors.New("seal: unknown or retired key")
	ErrForged     = errors.New("seal: authentication failed")
	ErrExpired    = errors.New("seal: expired")
	ErrInvalid    = errors.New("seal: invalid query")
	ErrDatabase   = errors.New("seal: key belongs to another database")
)

// Query is a sealed query.
type Query struct {
	Database string    `json:"db"`
	SQL      string    `json:"sql"`
	Params   []*string `json:"params"` // PostgreSQL text form; nil is NULL
	IssuedAt int64     `json:"iat"`    // Unix seconds
	Expires  int64     `json:"exp"`    // Unix seconds
}

// Fingerprint identifies the query and its parameters, independent of when
// it was sealed. A renewal keeps a subscription only when it matches.
func (q Query) Fingerprint() string {
	h := sha256.New()
	field := func(s string) {
		_, _ = fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	field(q.Database)
	field(q.SQL)
	_, _ = fmt.Fprintf(h, "%d;", len(q.Params))
	for _, p := range q.Params {
		if p == nil {
			_, _ = io.WriteString(h, "N")
			continue
		}
		_, _ = io.WriteString(h, "V")
		field(*p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ExpiresAt is the seal's expiry.
func (q Query) ExpiresAt() time.Time { return time.Unix(q.Expires, 0) }

// validate checks everything but the times.
func (q Query) validate() error {
	text := func(what, s string, limit int) error {
		switch {
		case len(s) > limit:
			return fmt.Errorf("%w: %s is %d bytes, limit %d", ErrInvalid, what, len(s), limit)
		case !utf8.ValidString(s):
			return fmt.Errorf("%w: %s is not UTF-8", ErrInvalid, what)
		case strings.IndexByte(s, 0) >= 0:
			return fmt.Errorf("%w: %s contains a NUL byte", ErrInvalid, what)
		}
		return nil
	}
	if q.Database == "" || strings.TrimSpace(q.SQL) == "" {
		return fmt.Errorf("%w: database and SQL are required", ErrInvalid)
	}
	if err := text("database", q.Database, api.LiveQueryDatabaseIDMaxBytes); err != nil {
		return err
	}
	if err := text("SQL", q.SQL, api.LiveQuerySQLMaxBytes); err != nil {
		return err
	}
	if len(q.Params) > api.LiveQueryParamsMax {
		return fmt.Errorf("%w: %d parameters, limit %d", ErrInvalid, len(q.Params), api.LiveQueryParamsMax)
	}
	for i, p := range q.Params {
		if p == nil {
			continue
		}
		if err := text(fmt.Sprintf("parameter %d", i+1), *p, api.LiveQueryParamMaxBytes); err != nil {
			return err
		}
	}
	return nil
}

// checkTimes checks the seal's lifetime against now.
func (q Query) checkTimes(now time.Time) error {
	skew := int64(api.LiveQuerySealClockSkew / time.Second)
	unix := now.Unix()
	switch {
	case q.Expires <= q.IssuedAt || q.Expires-q.IssuedAt > int64(api.LiveQuerySealMaxTTL/time.Second):
		return fmt.Errorf("%w: lifetime %d s, limit %d s", ErrInvalid, q.Expires-q.IssuedAt, int64(api.LiveQuerySealMaxTTL/time.Second))
	case q.IssuedAt > unix+skew:
		return fmt.Errorf("%w: issued %d s in the future", ErrInvalid, q.IssuedAt-unix)
	case unix > q.Expires+skew:
		return fmt.Errorf("%w %d s ago", ErrExpired, unix-q.Expires)
	}
	return nil
}

// Secret is a parsed per-database live secret.
type Secret struct {
	KID  string
	aead cipher.AEAD
}

// GenerateSecret returns a new secret in its customer-facing form.
func GenerateSecret() (string, error) {
	kid := make([]byte, kidLength/2)
	key := make([]byte, keyLength)
	if _, err := rand.Read(kid); err != nil {
		return "", fmt.Errorf("seal: generate key ID: %w", err)
	}
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("seal: generate key: %w", err)
	}
	return secretPrefix + "_" + hex.EncodeToString(kid) + "_" + base64.RawURLEncoding.EncodeToString(key), nil
}

// ParseSecret parses a secret's customer-facing form.
func ParseSecret(s string) (Secret, error) {
	parts := strings.SplitN(s, "_", 3)
	if len(parts) != 3 || parts[0] != secretPrefix || !validKID(parts[1]) {
		return Secret{}, fmt.Errorf("%w: secret", ErrMalformed)
	}
	material, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(material) != keyLength {
		return Secret{}, fmt.Errorf("%w: secret key material", ErrMalformed)
	}
	key, err := hkdf.Key(sha256.New, material, nil, hkdfInfo, keyLength)
	if err != nil {
		return Secret{}, fmt.Errorf("seal: derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Secret{}, fmt.Errorf("seal: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Secret{}, fmt.Errorf("seal: GCM: %w", err)
	}
	return Secret{KID: parts[1], aead: aead}, nil
}

func validKID(s string) bool {
	if len(s) != kidLength {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Seal seals q for ttl from now; a zero ttl means the default. It sets q's
// times.
func (s Secret) Seal(q Query, now time.Time, ttl time.Duration) (string, error) {
	if s.aead == nil {
		return "", fmt.Errorf("%w: secret not parsed", ErrMalformed)
	}
	if ttl == 0 {
		ttl = api.LiveQuerySealDefaultTTL
	}
	if ttl < time.Second || ttl > api.LiveQuerySealMaxTTL {
		return "", fmt.Errorf("%w: lifetime %s, limit %s", ErrInvalid, ttl, api.LiveQuerySealMaxTTL)
	}
	q.IssuedAt = now.Unix()
	q.Expires = now.Add(ttl).Unix()
	if q.Params == nil {
		q.Params = []*string{}
	}
	if err := q.validate(); err != nil {
		return "", err
	}
	nonce := make([]byte, nonceLength)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("seal: nonce: %w", err)
	}
	return s.seal(q, nonce)
}

// seal encodes q as JSON without HTML escaping, as JSON.stringify does, and
// encrypts it under nonce.
func (s Secret) seal(q Query, nonce []byte) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(q); err != nil {
		return "", fmt.Errorf("seal: encode query: %w", err)
	}
	plain := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(plain) > api.LiveQuerySealPayloadMaxBytes {
		return "", fmt.Errorf("%w: sealed query is %d bytes, limit %d", ErrInvalid, len(plain), api.LiveQuerySealPayloadMaxBytes)
	}
	header := tokenPrefix + "." + s.KID
	sealed := s.aead.Seal(append([]byte(nil), nonce...), nonce, plain, []byte(header))
	return header + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Key is one secret a key ring accepts.
type Key struct {
	Database string
	Secret   Secret
	// Retires, when set, is when the key stops opening seals: rotation keeps
	// the previous key until then, so outstanding seals and backends still
	// holding the old secret keep working.
	Retires time.Time
}

// Keyring opens seals with the keys of the databases an endpoint may serve.
type Keyring struct {
	keys map[string]Key
}

// NewKeyring builds a key ring. Key IDs are unique across databases.
func NewKeyring(keys ...Key) (*Keyring, error) {
	k := &Keyring{keys: make(map[string]Key, len(keys))}
	for _, key := range keys {
		if key.Secret.aead == nil || key.Database == "" {
			return nil, fmt.Errorf("%w: key ring entry", ErrMalformed)
		}
		if _, ok := k.keys[key.Secret.KID]; ok {
			return nil, fmt.Errorf("seal: duplicate key ID %s", key.Secret.KID)
		}
		k.keys[key.Secret.KID] = key
	}
	return k, nil
}

// Open authenticates and decodes a seal and checks its lifetime and limits.
func (k *Keyring) Open(token string, now time.Time) (Query, error) {
	maxToken := len(tokenPrefix) + kidLength + 2 +
		base64.RawURLEncoding.EncodedLen(nonceLength+api.LiveQuerySealPayloadMaxBytes+16)
	if len(token) > maxToken {
		return Query{}, fmt.Errorf("%w: %d bytes", ErrMalformed, len(token))
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix || !validKID(parts[1]) {
		return Query{}, fmt.Errorf("%w: header", ErrMalformed)
	}
	key, ok := k.keys[parts[1]]
	if !ok || (!key.Retires.IsZero() && !now.Before(key.Retires)) {
		return Query{}, ErrUnknownKey
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sealed) < nonceLength+key.Secret.aead.Overhead() {
		return Query{}, fmt.Errorf("%w: body", ErrMalformed)
	}
	plain, err := key.Secret.aead.Open(nil, sealed[:nonceLength], sealed[nonceLength:], []byte(parts[0]+"."+parts[1]))
	if err != nil {
		return Query{}, ErrForged
	}
	var q Query
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		return Query{}, fmt.Errorf("%w: payload: %w", ErrInvalid, err)
	}
	if q.Database != key.Database {
		return Query{}, ErrDatabase
	}
	if err := q.validate(); err != nil {
		return Query{}, err
	}
	if err := q.checkTimes(now); err != nil {
		return Query{}, err
	}
	return q, nil
}

// KeyID returns the key ID in a seal's clear header, or "" when the header is
// malformed. It authenticates nothing.
func KeyID(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 || parts[0] != tokenPrefix || !validKID(parts[1]) {
		return ""
	}
	return parts[1]
}

// ValidKeyID reports whether kid is a well-formed key ID: 16 lowercase hex
// digits.
func ValidKeyID(kid string) bool { return validKID(kid) }

// Opens reports whether the ring holds key ID kid and it has not retired.
func (k *Keyring) Opens(kid string, now time.Time) bool {
	key, ok := k.keys[kid]
	return ok && (key.Retires.IsZero() || now.Before(key.Retires))
}

// Len is the number of keys in the ring.
func (k *Keyring) Len() int { return len(k.keys) }

// Code is the live.error code for an Open or Seal error.
func Code(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "seal_expired"
	case errors.Is(err, ErrInvalid):
		return "seal_invalid"
	case errors.Is(err, ErrDatabase):
		return "forbidden"
	default:
		return "seal_rejected"
	}
}

// Args returns the parameters as query arguments: PostgreSQL parses each
// text value as its parameter's type, and nil is NULL.
func (q Query) Args() []any {
	out := make([]any, len(q.Params))
	for i, p := range q.Params {
		out[i] = p
	}
	return out
}
