package main

// service_caller_minter.go — ADR-206 minting surface for the caller assertion
// the node-local service proxy attaches to an internal service call.
//
// Key posture mirrors ADR-119's per-host internal-service key
// (cmd/schedd/internal_svc_minter.go), deliberately:
//
//   - The key is per HOST, not fleet-wide. cluster_signing_keys is Ed25519 and
//     would work, but schedd holds its sealed private half and this daemon
//     loads verifier public keys only. Putting the fleet private key in the
//     data-plane daemon on every node buys nothing here — an assertion needs
//     to be trustworthy, not fleet-signed — and widens the blast radius a lot.
//   - compute_node_keys (ADR-053) is per-node but ECDSA-P-256, owned by vmmd,
//     and scoped to CapacityReport signing. Not reusable without a second
//     algorithm and a second private-key holder.
//
// The whole path is gated on FAAS_SERVICE_CALLER_ASSERTIONS. Nothing verifies
// these yet (guest JWKS, runtime helper, and allow_callers policy are ADR-206
// follow-ups), so an operator with no verifier pays nothing: no key is loaded
// and no signature is computed.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicecaller"
)

const (
	serviceCallerEnabledEnv = "FAAS_SERVICE_CALLER_ASSERTIONS"
	serviceCallerKeyPathEnv = "FAAS_SERVICE_CALLER_KEY_PATH"
	defaultServiceCallerKey = "/etc/faas/secrets/service-caller/gatewayd.ed25519"
)

// serviceCallerAssertionsEnabled reports whether ADR-206 minting is on. Off is
// the default: the assertion has no consumer yet, and an unread signature on
// the request path should cost nothing.
func serviceCallerAssertionsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(serviceCallerEnabledEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// newServiceCallerMinter returns the proxy's mint seam, or nil when the
// feature is off or no key could be established.
//
// A nil return is not an error: ServiceProxy treats a nil minter as "attach
// nothing", so a key problem degrades to the pre-ADR-206 behaviour rather than
// failing every internal call. The log line is what makes that visible.
func newServiceCallerMinter(nodeID string, log *slog.Logger) gateway.ServiceCallerMinter {
	if !serviceCallerAssertionsEnabled() {
		return nil
	}
	priv, kid, err := loadServiceCallerKey(log)
	if err != nil {
		log.Error("gatewayd: service caller assertions enabled but no signing key; calls continue unsigned",
			"err", err, "path", serviceCallerKeyPath())
		return nil
	}
	log.Info("gatewayd: service caller assertions enabled", "kid", kid, "node", nodeID)
	return func(in gateway.ServiceCallerMintInput) (string, error) {
		return servicecaller.Mint(servicecaller.MintInput{
			CallerAppID:      in.CallerAppID,
			TargetAppID:      in.TargetAppID,
			AccountID:        in.AccountID,
			CallerInstanceID: in.CallerInstanceID,
			CallerEnv:        in.CallerEnv,
		}, priv, kid, servicecaller.MaxTTL, time.Now())
	}
}

func serviceCallerKeyPath() string {
	if path := strings.TrimSpace(os.Getenv(serviceCallerKeyPathEnv)); path != "" {
		return path
	}
	return defaultServiceCallerKey
}

// loadServiceCallerKey reads the per-host Ed25519 key, generating one on first
// boot if absent. Generation logs at WARN: a key that only exists on this
// host's disk is lost on reprovision, and every assertion it signed becomes
// unverifiable, so the operator needs to persist it deliberately.
func loadServiceCallerKey(log *slog.Logger) (ed25519.PrivateKey, string, error) {
	path := serviceCallerKeyPath()
	data, err := os.ReadFile(path) //nolint:gosec // operator-provisioned key path, same posture as ADR-119's per-host key.
	switch {
	case err == nil:
		priv, parseErr := parseServiceCallerKeyPEM(data)
		if parseErr != nil {
			return nil, "", parseErr
		}
		return priv, serviceCallerKid(priv), nil
	case errors.Is(err, os.ErrNotExist):
		priv, genErr := generateServiceCallerKey(path)
		if genErr != nil {
			return nil, "", genErr
		}
		log.Warn("gatewayd: generated a service-caller signing key on first boot; persist it or assertions signed by this node become unverifiable after a reprovision",
			"path", path)
		return priv, serviceCallerKid(priv), nil
	default:
		return nil, "", fmt.Errorf("read service caller key %q: %w", path, err)
	}
}

func generateServiceCallerKey(path string) (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate service caller key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal service caller key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create service caller key dir: %w", err)
	}
	blob := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	// 0600: the private half of an identity assertion. Written with O_EXCL so
	// two daemons racing on first boot cannot silently clobber each other's
	// key and leave assertions signed under a kid nobody published.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create service caller key %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(blob); err != nil {
		return nil, fmt.Errorf("write service caller key: %w", err)
	}
	return priv, nil
}

func parseServiceCallerKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("service caller key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse service caller key: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("service caller key is %T, want ed25519.PrivateKey", parsed)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("service caller key has wrong size %d", len(priv))
	}
	return priv, nil
}

// serviceCallerKid derives a stable key id from the public half, so the same
// key always publishes the same kid and a verifier can map kid → node without
// extra bookkeeping.
func serviceCallerKid(priv ed25519.PrivateKey) string {
	pub := priv.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}
