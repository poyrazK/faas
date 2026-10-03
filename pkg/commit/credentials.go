package commit

import (
	"errors"
	"net/url"
	"strings"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const credentialMaxBytes = 8192

// ValidateConnection accepts only a narrow TLS-verified PostgreSQL URL. Network
// destination policy must additionally be enforced by the relay's dialer before
// each connection, including DNS refresh. This check never opens a connection.
func ValidateConnection(raw string) error {
	if len(raw) == 0 || len(raw) > credentialMaxBytes {
		return errors.New("commit: invalid database connection")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || strings.Contains(u.Hostname(), "/") || u.User == nil || u.User.Username() == "" || u.Path == "" || u.Path == "/" || u.Fragment != "" {
		return errors.New("commit: invalid database connection")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("commit: invalid database options")
	}
	for key, values := range q {
		if len(values) != 1 || (key != "sslmode" && key != "connect_timeout") {
			return errors.New("commit: unsupported database option")
		}
	}
	if q.Get("sslmode") != "verify-full" {
		return errors.New("commit: database TLS verification is required")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.TLSConfig == nil || cfg.ConnConfig.TLSConfig.InsecureSkipVerify {
		return errors.New("commit: invalid database TLS configuration")
	}
	return nil
}

func SealConnection(recipient *age.X25519Recipient, sourceID, raw string) ([]byte, error) {
	if _, err := uuid.Parse(sourceID); err != nil {
		return nil, errors.New("commit: invalid source ID")
	}
	if err := ValidateConnection(raw); err != nil {
		return nil, err
	}
	return secretbox.SealBytes(recipient, "gregale.commit.database."+sourceID, []byte(raw), credentialMaxBytes)
}

func OpenConnection(identities []*age.X25519Identity, sourceID string, blob []byte) (string, error) {
	namespace, plain, err := secretbox.OpenBytesMulti(identities, blob)
	if err != nil || namespace != "gregale.commit.database."+sourceID {
		return "", errors.New("commit: database credential unavailable")
	}
	raw := string(plain)
	if err := ValidateConnection(raw); err != nil {
		return "", err
	}
	return raw, nil
}
