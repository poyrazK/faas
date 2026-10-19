package mailoutbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const (
	VerificationOriginEnv           = "FAAS_INTERNAL_MAIL_VERIFICATION_ORIGIN"
	VerificationIdentityEnv         = "FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH"
	VerificationPreviousIdentityEnv = "FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH"
	VerificationTemplate            = "email_verification"
)

type credentialConfig struct {
	origin, keyID string
	writer        *age.X25519Identity
	readers       map[string]*age.X25519Identity
	sender        recoverySender
	enabled       func() bool
}

func (s *Service) verificationEnabled() bool {
	return s.Enabled() && s.verification != nil && s.verification.enabled != nil && s.verification.enabled()
}

// ConfigureVerification is an independent default-off capability. No key path,
// public origin, ciphertext or token state is inspected before its exact gate.
func (s *Service) ConfigureVerification(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.VerificationOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("verification outbox requires the configured producer outbox")
	}
	origin, err := verificationOrigin(getenv(VerificationOriginEnv))
	if err != nil {
		return err
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return err
	}
	on := func() bool { return s.Enabled() && internalmail.VerificationOutboxEnabled(getenv) }
	keys, err := s.queries.VerificationKeyReferences(ctx, s.pool, q.VerificationKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("verification outbox encryption metadata schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("verification outbox is missing a pending payload's reader identity")
		}
	}
	if !on() {
		return internalmail.ErrDisabled
	}
	s.verification = &credentialConfig{origin: origin, keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithVerificationOutbox(on), enabled: on}
	return nil
}

func credentialReaders(getenv func(string) string) (string, *age.X25519Identity, map[string]*age.X25519Identity, error) {
	writer, err := readVerificationIdentity(getenv(VerificationIdentityEnv))
	if err != nil {
		return "", nil, nil, errors.New("verification outbox current encryption identity unavailable")
	}
	keyID := verificationKeyID(writer)
	readers := map[string]*age.X25519Identity{keyID: writer}
	if path := getenv(VerificationPreviousIdentityEnv); path != "" {
		previous, err := readVerificationIdentity(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", nil, nil, errors.New("verification outbox previous encryption identity unavailable")
		}
		if previous != nil {
			id := verificationKeyID(previous)
			if id == keyID {
				return "", nil, nil, errors.New("verification outbox reader identities must be distinct")
			}
			readers[id] = previous
		}
	}
	return keyID, writer, readers, nil
}

func verificationOrigin(raw string) (string, error) {
	if len(raw) > api.InternalMailVerificationOriginMaxBytes || strings.ContainsAny(raw, "\r\n\x00\\#") {
		return "", internalmail.ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return "", errors.New("verification outbox requires a canonical HTTPS origin")
	}
	if u.Port() != "" {
		port, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || port == 0 {
			return "", internalmail.ErrInvalid
		}
	}
	// Normalize only the configured origin; request Host and forwarding headers
	// never select a credential destination for the durable path.
	return "https://" + strings.ToLower(u.Host), nil
}

func verificationKeyID(id *age.X25519Identity) string {
	digest := sha256.Sum256([]byte(id.Recipient().String()))
	return hex.EncodeToString(digest[:])
}

func readVerificationIdentity(path string) (*age.X25519Identity, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, internalmail.ErrInvalid
	}
	f, err := os.Open(path) //nolint:forbidigo // Operator-provisioned LoadCredential path; regular-file/private-mode checks run before reading, and no customer input selects this path.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0o077 != 0 {
		return nil, internalmail.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.InternalMailVerificationIdentityMaxBytes+1))
	defer clear(raw)
	if err != nil || len(raw) > api.InternalMailVerificationIdentityMaxBytes {
		return nil, internalmail.ErrInvalid
	}
	return age.ParseX25519Identity(strings.TrimSpace(string(raw)))
}
