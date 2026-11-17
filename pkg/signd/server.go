// Package signd is the control-plane artifact signer (ADR-20261109170000).
//
// It holds the fleet signing key, which compute nodes no longer need. A
// compute node's imaged asks it to sign each app layer it publishes; signd
// signs only when the caller is the node building that deployment right now,
// the key is one the build produces, and the key has no other signature.
package signd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	signdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/signd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrNoClaim is returned by Store.BuildClaim when the deployment does not
// exist, is deleted, or has no image preparation.
var ErrNoClaim = errors.New("signd: no build claim for the deployment")

// Claim is what signd reads about a deployment before it signs.
type Claim struct {
	Status   state.DeploymentStatus
	Sidecars []byte
	AppSlug  string
	// NodeName and Phase are the image preparation's.
	NodeName string
	Phase    state.ImagePreparationPhase
}

// Record is one signed artifact.
type Record struct {
	StorageKey, Digest, DeploymentID, NodeName, Format string
	Bytes                                              int64
}

// Store is signd's database: build claims to read, and the one table it
// writes.
type Store interface {
	BuildClaim(ctx context.Context, deploymentID string) (Claim, error)
	// RecordSignature inserts r unless its key already has a row. It
	// returns the row stored for the key and whether it was r's insert.
	RecordSignature(ctx context.Context, r Record) (Record, bool, error)
}

// Server serves SignArtifact.
type Server struct {
	signdpb.UnimplementedSigndServer
	store   Store
	key     *ecdsa.PrivateKey
	objects storage.StorageBackend
	log     *slog.Logger
	// peerNode names the calling compute node; wire.PeerCN in production.
	peerNode func(context.Context) (string, error)
}

// NewServer returns a signer. key is the fleet signing key; objects is the
// artifacts store the signatures go to.
func NewServer(store Store, key *ecdsa.PrivateKey, objects storage.StorageBackend, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{store: store, key: key, objects: objects, log: log, peerNode: wire.PeerCN}
}

// SignArtifact implements signdpb.SigndServer.
func (s *Server) SignArtifact(ctx context.Context, req *signdpb.SignArtifactRequest) (*signdpb.SignArtifactResponse, error) {
	node, err := s.peerNode(ctx)
	if err != nil || strings.TrimSpace(node) == "" {
		return nil, rpcErr(codes.Unauthenticated, "signer refused the caller: the caller presented no node identity")
	}
	rec, err := s.authorize(ctx, node, req)
	if err != nil {
		return nil, err
	}
	stored, inserted, err := s.store.RecordSignature(ctx, rec)
	if err != nil {
		s.log.Error("signd: record signature", "key", rec.StorageKey, "node", node, "err", err)
		return nil, rpcErr(codes.Unavailable, "signer could not record the signature: retry the request")
	}
	// A retry after a lost reply finds its own row and signs again; any
	// other row means the key was already signed for something else.
	if !inserted && stored != rec {
		s.log.Warn("signd: refused a second signature", "key", rec.StorageKey, "node", node,
			"deployment", rec.DeploymentID, "signed_by", stored.NodeName, "signed_for", stored.DeploymentID)
		return nil, refuse("the storage key is already signed")
	}
	doc, err := cosign.SignV2(s.key, rec.StorageKey, rec.Digest)
	if err != nil {
		s.log.Error("signd: sign", "key", rec.StorageKey, "err", err)
		return nil, rpcErr(codes.Internal, "signer could not sign the artifact: see the signd log")
	}
	sigKey := cosign.SigKeyFor(rec.StorageKey)
	if err := s.objects.Put(ctx, sigKey, bytes.NewReader(doc)); err != nil {
		s.log.Error("signd: write signature", "key", sigKey, "err", err)
		return nil, rpcErr(codes.Unavailable, "signer could not write the signature: retry the request")
	}
	s.log.Info("signd: signed artifact", "key", rec.StorageKey, "node", node, "deployment", rec.DeploymentID, "bytes", rec.Bytes)
	return &signdpb.SignArtifactResponse{SignatureKey: sigKey}, nil
}

// authorize checks a request against its deployment's build claim and
// returns the record to store.
func (s *Server) authorize(ctx context.Context, node string, req *signdpb.SignArtifactRequest) (Record, error) {
	parsed, err := uuid.Parse(req.GetDeploymentId())
	if err != nil {
		return Record{}, rpcErr(codes.InvalidArgument, "invalid signature request: deployment_id must be a UUID")
	}
	if !cosign.ValidDigestHex(req.GetDigest()) || req.GetBytes() <= 0 {
		return Record{}, rpcErr(codes.InvalidArgument, "invalid signature request: digest must be a lowercase hex SHA-256 and bytes positive")
	}
	// The canonical lowercase form: storage keys and stored rows use it.
	depID := parsed.String()
	claim, err := s.store.BuildClaim(ctx, depID)
	if errors.Is(err, ErrNoClaim) {
		return Record{}, refuse("the deployment has no build in progress")
	}
	if err != nil {
		s.log.Error("signd: read build claim", "deployment", depID, "err", err)
		return Record{}, rpcErr(codes.Unavailable, "signer could not read the build: retry the request")
	}
	if reason := checkClaim(claim, node, depID, req.GetStorageKey()); reason != "" {
		s.log.Warn("signd: refused", "node", node, "deployment", depID, "key", req.GetStorageKey(), "reason", reason)
		return Record{}, refuse(reason)
	}
	return Record{StorageKey: req.GetStorageKey(), Digest: req.GetDigest(), Bytes: req.GetBytes(),
		DeploymentID: depID, NodeName: node, Format: recordFormatV2}, nil
}

// recordFormatV2 is artifact_signatures.signature_format for a v2 signature.
const recordFormatV2 = "v2"

// checkClaim returns why claim does not let node sign key, or "".
func checkClaim(claim Claim, node, deploymentID, key string) string {
	switch {
	case claim.NodeName != node:
		return "the caller is not the node building the deployment"
	case claim.Phase != state.ImagePreparing:
		return "the deployment's layer is already published"
	case claim.Status.IsTerminal() || claim.Status == state.DeployLive:
		return "the deployment is not building"
	}
	keys, err := buildKeys(claim.AppSlug, deploymentID, claim.Sidecars)
	if err != nil {
		return "the deployment's sidecars are unreadable"
	}
	if !keys[key] {
		return "the storage key is not one the deployment's build produces"
	}
	return ""
}

// buildKeys is every layer key a deployment's build publishes: the app
// layer and one per sidecar. signd derives them; it never trusts a key as
// given.
func buildKeys(slug, deploymentID string, sidecars []byte) (map[string]bool, error) {
	keys := map[string]bool{storagekey.AppLayer(slug, deploymentID): true}
	if len(sidecars) == 0 || string(sidecars) == "null" {
		return keys, nil
	}
	var list api.Sidecars
	if err := json.Unmarshal(sidecars, &list); err != nil {
		return nil, fmt.Errorf("decode sidecars: %w", err)
	}
	for _, sc := range list {
		keys[storagekey.AppSidecarLayer(slug, deploymentID, sc.Name)] = true
	}
	return keys, nil
}

func refuse(reason string) error {
	return rpcErr(codes.PermissionDenied, "signer refused the artifact: "+reason)
}

// rpcErr is a gRPC status for imaged. signd's errors never reach a
// customer, so they carry no problem detail envelope.
func rpcErr(c codes.Code, msg string) error { return status.Error(c, msg) }
