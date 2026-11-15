package secretd

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"time"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrUnknownPostgresCredential = errors.New("no live leased staged PostgreSQL credential")

type PostgresCredential struct {
	AccountID, AppID, Key, Access, ProviderIdentity, Ref, Kid, ValueHash string
	Ciphertext                                                           []byte
	Major                                                                int
}

type postgresVerificationStore interface {
	PostgresCutoverCredential(context.Context, string, string, string, string) (PostgresCredential, error)
	RecordPostgresVerification(context.Context, string, string, string, string) error
}

// PostgresClientVerifier admits the dedicated control-plane leaf at TLS only.
// Every other RPC still resolves its caller as an active compute node.
type PostgresClientVerifier struct{ Nodes wire.NodeVerifier }

func (v PostgresClientVerifier) LookupCN(cn string) error {
	if cn == pki.ApidPostgresVerifierClient {
		return nil
	}
	if v.Nodes == nil {
		return errors.New("unregistered secret client")
	}
	return v.Nodes.LookupCN(cn)
}

// WithPostgresVerification adds read-only SQL probing. The fingerprint key is
// a control-plane credential; it is never sent in an RPC or to compute nodes.
func (s *Server) WithPostgresVerification(key []byte, probe func(context.Context, string, managedpostgres.CredentialAccess, int) error) *Server {
	s.postgresHMAC = append([]byte(nil), key...)
	s.postgresProbe = probe
	return s
}

func (s *Server) VerifyPostgresCutover(ctx context.Context, req *secretdpb.VerifyPostgresCutoverRequest) (response *secretdpb.VerifyPostgresCutoverResponse, resultErr error) {
	defer func() {
		if resultErr == nil {
			s.metrics.open(rpcVerifyPostgresCutover, "verification")
			return
		}
		reason := ReasonEnvelope
		if status.Code(resultErr) == codes.PermissionDenied {
			reason = ReasonNotAuthorized
		}
		s.metrics.refuse(rpcVerifyPostgresCutover, reason)
	}()
	name, err := s.peerNode(ctx)
	if err != nil || name != pki.ApidPostgresVerifierClient {
		return nil, rpcErr(codes.PermissionDenied, "PostgreSQL verification requires the control-plane verifier")
	}
	store, ok := s.store.(postgresVerificationStore)
	if !ok || s.postgresProbe == nil || len(s.postgresHMAC) == 0 {
		return nil, rpcErr(codes.Unavailable, "PostgreSQL verifier is not configured")
	}
	if req == nil || req.AccountId == "" || req.CutoverId == "" || req.CredentialId == "" || req.LeaseToken == "" || len(req.CiphertextSha256) != sha256.Size {
		return nil, rpcErr(codes.InvalidArgument, "a staged credential and live cutover lease are required")
	}
	credential, err := store.PostgresCutoverCredential(ctx, req.AccountId, req.CutoverId, req.CredentialId, req.LeaseToken)
	if errors.Is(err, ErrUnknownPostgresCredential) {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential lease or target is no longer current")
	}
	if err != nil {
		return nil, rpcErr(codes.Unavailable, "could not read staged credential")
	}
	sum := sha256.Sum256(credential.Ciphertext)
	expectedRef, err := credentialdelivery.Ref(managedpostgres.Binding{ID: req.CredentialId, CredentialGeneration: 1})
	if err != nil || credential.Ref != expectedRef {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential reference is invalid")
	}
	if credential.AccountID != req.AccountId || subtle.ConstantTimeCompare(sum[:], req.CiphertextSha256) != 1 || credential.Kid != req.Kid ||
		credential.Ref != req.CredentialRef || credential.ValueHash != req.ValueHash {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential changed")
	}
	// Audit before opening. A failed audit prevents decryption and SQL access.
	if err := store.RecordPostgresVerification(ctx, credential.AccountID, credential.AppID, req.CutoverId, req.CredentialId); err != nil {
		return nil, rpcErr(codes.Unavailable, "could not audit credential verification")
	}
	matched := false
	for _, id := range s.identities {
		if id != nil && id.Recipient().String() == credential.Kid {
			matched = true
			break
		}
	}
	if !matched {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential key is unavailable")
	}
	values, binding, err := secretbox.OpenMultiBound(s.identities, credential.Ciphertext)
	if err != nil || binding.Check(credential.AccountID) != nil || len(values) != 1 {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential envelope is invalid")
	}
	defer clear(values)
	value, ok := values[credential.Key]
	if !ok || len(value) == 0 || len(value) > 32<<10 {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential value is invalid")
	}
	fingerprint, err := secretbox.ValueFingerprint([]byte(value), s.postgresHMAC)
	if err != nil || subtle.ConstantTimeCompare([]byte(fingerprint), []byte(credential.ValueHash)) != 1 {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential fingerprint is invalid")
	}
	config, err := managedpostgres.CredentialSQLConfig(value)
	if err != nil || config.User != credential.ProviderIdentity {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential identity is invalid")
	}
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.postgresProbe(probe, value, managedpostgres.CredentialAccess(credential.Access).Privileges(), credential.Major); err != nil {
		return nil, rpcErr(codes.FailedPrecondition, "staged credential SQL verification failed")
	}
	if err := probe.Err(); err != nil {
		return nil, rpcErr(codes.DeadlineExceeded, "staged credential SQL verification timed out")
	}
	return &secretdpb.VerifyPostgresCutoverResponse{}, nil
}
