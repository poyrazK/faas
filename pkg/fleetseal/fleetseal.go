// Package fleetseal migrates and verifies the dedicated fleet-wide age
// domain. Plaintext exists only in process memory between Open and Seal.
package fleetseal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	ProbeNamespace          = "app_secret"
	ProbeKey                = "GREGALE_FLEET_SEAL_PROBE"
	ProbeValue              = "gregale-fleet-seal-domain-v1"
	clusterSigningNamespace = "internal_svc"
	maxProbeBytes           = 256
)

type Store interface {
	LoadClusterSigningKey(context.Context) (state.ClusterSigningKey, error)
	ResealClusterSigningKey(context.Context, string, []byte, []byte) error
	ListAppSecretsForRekey(context.Context, int, string) ([]state.AppSecret, error)
	ResealAppSecretForFleet(context.Context, state.AppSecret, string, []byte) error
	LoadFleetSealProbe(context.Context) (state.FleetSealProbe, error)
	UpsertFleetSealProbe(context.Context, string, []byte) error
}

type MigrationReport struct {
	Recipient       string `json:"recipient"`
	ClusterKID      string `json:"cluster_kid"`
	SecretsScanned  int    `json:"secrets_scanned"`
	SecretsResealed int    `json:"secrets_resealed"`
	ProbeWritten    bool   `json:"probe_written"`
}

type VerificationReport struct {
	Ready            bool   `json:"ready"`
	FleetRecipient   string `json:"fleet_recipient"`
	HostRecipient    string `json:"host_recipient,omitempty"`
	ClusterKID       string `json:"cluster_kid"`
	ProbeOK          bool   `json:"probe_ok"`
	JWTRoundTripOK   bool   `json:"jwt_round_trip_ok"`
	HostIdentityKept bool   `json:"host_identity_kept"`
}

// Migrate re-seals the singleton cluster key and every customer app secret
// to fleet. Compare-and-swap writes retain a valid encrypted old or new value
// across interruption and reject concurrent customer/key rotation.
func Migrate(ctx context.Context, store Store, fleet *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || fleet == nil {
		return MigrationReport{}, errors.New("fleetseal: store and fleet identity are required")
	}
	recipient := fleet.Recipient()
	report := MigrationReport{Recipient: recipient.String()}
	openers := dedupeIdentities(append([]*age.X25519Identity{fleet}, legacy...))

	cluster, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		return report, fmt.Errorf("fleetseal: load cluster key: %w", err)
	}
	report.ClusterKID = cluster.KeyID
	ns, plaintext, fleetErr := secretbox.OpenBytes(fleet, cluster.SealedBlob)
	if fleetErr == nil && ns == clusterSigningNamespace {
		zero(plaintext)
	} else {
		if fleetErr != nil {
			var openErr error
			ns, plaintext, openErr = secretbox.OpenBytesMulti(openers, cluster.SealedBlob)
			_ = ns // legacy namespaces are normalized below.
			if openErr != nil {
				return report, fmt.Errorf("fleetseal: cluster key cannot be opened by fleet or legacy identities: %w", openErr)
			}
		}
		sealed, sealErr := secretbox.SealBytes(recipient, clusterSigningNamespace, plaintext, len(plaintext)+1)
		zero(plaintext)
		if sealErr != nil {
			return report, fmt.Errorf("fleetseal: reseal cluster key: %w", sealErr)
		}
		if err := store.ResealClusterSigningKey(ctx, cluster.KeyID, cluster.SealedBlob, sealed); err != nil {
			return report, err
		}
	}

	const batchSize = 100
	cursor := ""
	seen := make(map[string]struct{})
	for {
		rows, err := store.ListAppSecretsForRekey(ctx, batchSize, cursor)
		if err != nil {
			return report, fmt.Errorf("fleetseal: list customer secrets: %w", err)
		}
		progressed := false
		for _, row := range rows {
			key := row.AccountID + "\x00" + row.AppID + "\x00" + row.Scope + "\x00" + row.Key
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			progressed = true
			report.SecretsScanned++
			cursor = row.AccountID + "|" + row.AppID + "|" + row.Scope + "|" + row.Key
			if row.Kid == report.Recipient {
				if env, fleetErr := secretbox.Open(fleet, row.Ciphertext); fleetErr == nil {
					for k := range env {
						env[k] = ""
						delete(env, k)
					}
					continue
				}
			}
			env, openErr := secretbox.OpenMulti(openers, row.Ciphertext)
			if openErr != nil {
				return report, fmt.Errorf("fleetseal: unseal app=%s scope=%s key=%s: %w", row.AppID, row.Scope, row.Key, openErr)
			}
			sealed, sealErr := secretbox.Seal(recipient, env)
			for k := range env {
				env[k] = ""
				delete(env, k)
			}
			if sealErr != nil {
				return report, fmt.Errorf("fleetseal: reseal app=%s scope=%s key=%s: %w", row.AppID, row.Scope, row.Key, sealErr)
			}
			if err := store.ResealAppSecretForFleet(ctx, row, report.Recipient, sealed); err != nil {
				return report, fmt.Errorf("fleetseal: persist app=%s scope=%s key=%s: %w", row.AppID, row.Scope, row.Key, err)
			}
			report.SecretsResealed++
		}
		if !progressed || len(rows) < batchSize {
			break
		}
	}

	probe, err := secretbox.SealOne(recipient, ProbeKey, ProbeValue, maxProbeBytes)
	if err != nil {
		return report, err
	}
	if err := store.UpsertFleetSealProbe(ctx, report.Recipient, probe); err != nil {
		return report, err
	}
	report.ProbeWritten = true
	return report, nil
}

// Verify proves the customer-path probe and cluster signing key using only the
// shared fleet identity. Passing a host identity additionally proves that node
// adoption retained a distinct per-host identity.
func Verify(ctx context.Context, store Store, fleet, host *age.X25519Identity) (VerificationReport, error) {
	if store == nil || fleet == nil {
		return VerificationReport{}, errors.New("fleetseal: store and fleet identity are required")
	}
	report := VerificationReport{FleetRecipient: fleet.Recipient().String()}
	if host != nil {
		report.HostRecipient = host.Recipient().String()
		report.HostIdentityKept = report.HostRecipient != report.FleetRecipient
		if !report.HostIdentityKept {
			return report, errors.New("fleetseal: host identity was replaced by fleet identity")
		}
	}
	probe, err := store.LoadFleetSealProbe(ctx)
	if err != nil {
		return report, fmt.Errorf("fleetseal: load probe: %w", err)
	}
	if probe.Recipient != report.FleetRecipient {
		return report, fmt.Errorf("fleetseal: probe recipient %s does not match staged fleet recipient %s", probe.Recipient, report.FleetRecipient)
	}
	env, err := secretbox.Open(fleet, probe.SealedBlob)
	if err != nil {
		return report, fmt.Errorf("fleetseal: customer-path probe decrypt failed: %w", err)
	}
	if env[ProbeKey] != ProbeValue {
		return report, errors.New("fleetseal: customer-path probe value mismatch")
	}
	report.ProbeOK = true

	cluster, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		return report, fmt.Errorf("fleetseal: load cluster key: %w", err)
	}
	ns, plaintext, err := secretbox.OpenBytes(fleet, cluster.SealedBlob)
	if err != nil {
		return report, fmt.Errorf("fleetseal: cluster signing key is not sealed to fleet identity: %w", err)
	}
	if ns != clusterSigningNamespace {
		zero(plaintext)
		return report, fmt.Errorf("fleetseal: cluster signing namespace %q, want %s", ns, clusterSigningNamespace)
	}
	priv, pub, err := parseAndMatchClusterKey(plaintext, cluster.PublicKeyPEM, cluster.KeyID)
	zero(plaintext)
	if err != nil {
		return report, err
	}
	report.ClusterKID = cluster.KeyID
	token, err := internalsvc.Mint("schedd", 30*time.Second, map[string]any{"fleet_probe": true}, priv, cluster.KeyID)
	if err != nil {
		return report, fmt.Errorf("fleetseal: mint shared-kid token: %w", err)
	}
	if subject, err := internalsvc.Verify(token, map[string]ed25519.PublicKey{"schedd": pub}); err != nil {
		return report, fmt.Errorf("fleetseal: verify shared-kid token: %w", err)
	} else if subject != "schedd" {
		return report, fmt.Errorf("fleetseal: verify shared-kid token returned subject %q", subject)
	}
	report.JWTRoundTripOK = true
	report.Ready = report.ProbeOK && report.JWTRoundTripOK && (host == nil || report.HostIdentityKept)
	return report, nil
}

func parseAndMatchClusterKey(privatePEM []byte, publicPEM, kid string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	privateBlock, _ := pem.Decode(privatePEM)
	if privateBlock == nil {
		return nil, nil, errors.New("fleetseal: cluster private key is not PEM")
	}
	parsedPrivate, err := x509.ParsePKCS8PrivateKey(privateBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("fleetseal: parse cluster private key: %w", err)
	}
	priv, ok := parsedPrivate.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, errors.New("fleetseal: cluster private key is not Ed25519")
	}
	publicBlock, _ := pem.Decode([]byte(publicPEM))
	if publicBlock == nil {
		return nil, nil, errors.New("fleetseal: cluster public key is not PEM")
	}
	parsedPublic, err := x509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("fleetseal: parse cluster public key: %w", err)
	}
	pub, ok := parsedPublic.(ed25519.PublicKey)
	if !ok || !bytes.Equal(pub, priv.Public().(ed25519.PublicKey)) {
		return nil, nil, errors.New("fleetseal: cluster public/private key mismatch")
	}
	if derived := internalsvc.KidFromPub(pub); derived != kid {
		return nil, nil, fmt.Errorf("fleetseal: cluster kid %s does not match derived %s", kid, derived)
	}
	return priv, pub, nil
}

func dedupeIdentities(ids []*age.X25519Identity) []*age.X25519Identity {
	seen := make(map[string]struct{})
	out := make([]*age.X25519Identity, 0, len(ids))
	for _, id := range ids {
		if id == nil {
			continue
		}
		key := id.Recipient().String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	return out
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
