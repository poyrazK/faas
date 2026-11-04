package edgecert

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/mholt/acmez/v3"
	"github.com/mholt/acmez/v3/acme"
	"go.uber.org/zap"
)

// LetsEncrypt orders certificates from Let's Encrypt with the DNS-01
// challenge, through certmagic's DNS-01 solver and the ACME client under it
// (ADR-100's issuer stack). It drives that client directly because the
// account is ours: a fixed key the operator keeps, registered once, whose
// URI the zone's CAA records name (RFC 8657), which certmagic's issuer
// neither registers for a given key nor reports.
type LetsEncrypt struct {
	ca      string
	client  *acmez.Client
	contact []string
	key     crypto.Signer

	mu      sync.Mutex
	account acme.Account
}

// NewLetsEncrypt returns the orderer for ca (CALetsEncrypt or
// CALetsEncryptStaging) with the account whose private key accountKeyPEM
// holds. email, when set, is the account's contact. dns publishes the
// challenges.
func NewLetsEncrypt(ca string, accountKeyPEM []byte, email string, dns certmagic.DNSProvider, log *slog.Logger) (*LetsEncrypt, error) {
	directory, err := directoryFor(ca)
	if err != nil {
		return nil, err
	}
	key, err := parseAccountKey(accountKeyPEM)
	if err != nil {
		return nil, err
	}
	if dns == nil {
		return nil, errors.New("edgecert: no DNS provider for the DNS-01 challenge")
	}
	var contact []string
	if email != "" {
		contact = []string{"mailto:" + email}
	}
	return &LetsEncrypt{
		ca:      ca,
		contact: contact,
		key:     key,
		client: &acmez.Client{
			Client: &acme.Client{
				Directory:  directory,
				HTTPClient: &http.Client{Timeout: 30 * time.Second},
				UserAgent:  "gregale-edgednsd",
				Logger:     log,
			},
			ChallengeSolvers: map[string]acmez.Solver{
				acme.ChallengeTypeDNS01: &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{DNSProvider: dns, Logger: zap.NewNop()}},
			},
		},
	}, nil
}

// CA names the CA this orderer uses.
func (l *LetsEncrypt) CA() string { return l.ca }

// Order registers the account if it is new, then orders a certificate for
// names signed with key.
func (l *LetsEncrypt) Order(ctx context.Context, key crypto.Signer, names []string) ([]byte, string, error) {
	account, err := l.Account(ctx)
	if err != nil {
		return nil, "", err
	}
	certs, err := l.client.ObtainCertificateForSANs(ctx, account, key, names)
	if err != nil {
		return nil, "", err
	}
	if len(certs) == 0 || len(certs[0].ChainPEM) == 0 {
		return nil, "", errors.New("edgecert: the CA returned no certificate")
	}
	return certs[0].ChainPEM, account.Location, nil
}

// Account returns the ACME account, registering it on first use. Posting a
// known key to newAccount returns the existing account (RFC 8555 §7.3.1), so
// a restart finds the same account and URI.
func (l *LetsEncrypt) Account(ctx context.Context) (acme.Account, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.account.Location != "" {
		return l.account, nil
	}
	account, err := l.client.NewAccount(ctx, acme.Account{Contact: l.contact, TermsOfServiceAgreed: true, PrivateKey: l.key})
	if err != nil {
		return acme.Account{}, fmt.Errorf("edgecert: register the ACME account: %w", err)
	}
	if account.Location == "" {
		return acme.Account{}, errors.New("edgecert: the CA returned no account URI")
	}
	l.account = account
	return account, nil
}

func directoryFor(ca string) (string, error) {
	switch ca {
	case CALetsEncrypt:
		return certmagic.LetsEncryptProductionCA, nil
	case CALetsEncryptStaging:
		return certmagic.LetsEncryptStagingCA, nil
	}
	return "", fmt.Errorf("edgecert: unknown CA %q (want %s or %s)", ca, CALetsEncrypt, CALetsEncryptStaging)
}

// parseAccountKey reads the account's ECDSA or Ed25519 private key, PKCS #8
// or SEC 1.
func parseAccountKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("edgecert: the ACME account key is not PEM")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("edgecert: parse the ACME account key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("edgecert: the ACME account key cannot sign")
	}
	return signer, nil
}
