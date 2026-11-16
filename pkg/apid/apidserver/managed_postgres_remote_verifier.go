package apidserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/wire"
)

func configurePostgresCutoverVerifier(ctx context.Context, s *appSecretCredentialSink, getenv func(string) string) error {
	target := getenv("FAAS_APID_SECRETD_TARGET")
	if target == "" {
		return nil
	} // fleet-key verification remains compatible
	parsed, err := wire.ParseTarget(target)
	if err != nil || (parsed.Scheme != wire.SchemeTCP && parsed.Scheme != wire.SchemeDNS) {
		return fmt.Errorf("PostgreSQL verifier requires a TLS TCP or DNS target")
	}
	cert, key, ca := getenv("FAAS_APID_SECRETD_TLS_CERT_PATH"), getenv("FAAS_APID_SECRETD_TLS_KEY_PATH"), getenv("FAAS_APID_SECRETD_TLS_CA_PATH")
	if strings.TrimSpace(cert) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(ca) == "" {
		return fmt.Errorf("PostgreSQL verifier requires its TLS certificate, key and CA paths")
	}
	tls, err := wire.LoadClientTLSConfigWithPrefix("apid_secretd_", cert, key, ca)
	if err != nil {
		return err
	}
	if tls == nil {
		return fmt.Errorf("PostgreSQL verifier requires mutual TLS")
	}
	tls.ServerName = pki.SecretdServerName
	conn, err := wire.Dial(ctx, target, tls)
	if err != nil {
		return err
	}
	// grpc.NewClient is lazy. Keep the connection for the service lifetime;
	// the sink owns the verifier and the daemon process owns the sink.
	s.cutoverProbe = postgresRemoteVerifier(secretdpb.NewSecretdClient(conn))
	return nil
}

func postgresRemoteVerifier(client secretdpb.SecretdClient) func(context.Context, managedpostgres.Cutover, managedpostgres.CutoverCredential, managedpostgres.Database) error {
	return func(ctx context.Context, c managedpostgres.Cutover, m managedpostgres.CutoverCredential, target managedpostgres.Database) error {
		if c.Target.ID != target.ID || c.AccountID != target.AccountID || c.LeaseToken == "" {
			return managedpostgres.ErrConflict
		}
		sum := sha256.Sum256(m.Sealed.Ciphertext)
		_, err := client.VerifyPostgresCutover(ctx, &secretdpb.VerifyPostgresCutoverRequest{AccountId: c.AccountID, CutoverId: c.ID, CredentialId: m.ID,
			LeaseToken: c.LeaseToken, CiphertextSha256: sum[:], Kid: m.Sealed.Kid, ValueHash: m.Sealed.ValueHash, CredentialRef: m.Sealed.Ref})
		if err != nil {
			return fmt.Errorf("staged credential verification service: %w", managedpostgres.ErrUnavailable)
		}
		return nil
	}
}
