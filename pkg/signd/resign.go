package signd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/storage"
)

// StoredLayer is one signed layer a live deployment can boot.
type StoredLayer struct {
	DeploymentID, StorageKey string
}

// ResignStore lists the stored layers and records their signatures.
type ResignStore interface {
	StoredLayers(ctx context.Context) ([]StoredLayer, error)
	RecordSignature(ctx context.Context, r Record) (Record, bool, error)
}

// ResignNode is artifact_signatures.node_name for rows the re-sign pass
// writes: the layer was built before signd, by a node it cannot name.
const ResignNode = "resign"

// ResignProblem is a layer the pass left alone.
type ResignProblem struct {
	StorageKey   string `json:"storage_key"`
	DeploymentID string `json:"deployment_id"`
	Reason       string `json:"reason"`
}

// ResignReport counts what the pass found and did.
type ResignReport struct {
	DryRun bool `json:"dry_run"`
	// Converted held a valid v1 signature, now v2 (or would be, dry run).
	Converted int `json:"converted"`
	// Current already held a valid v2 signature.
	Current int `json:"current"`
	// Recorded rows written to artifact_signatures.
	Recorded int             `json:"recorded"`
	Problems []ResignProblem `json:"problems,omitempty"`
}

// Resign converts every stored layer's v1 signature to a v2 one signed with
// key, and records app layers in artifact_signatures (ADR-20261109170000 §5
// step 3). A signature that does not verify under key, a missing signature
// and a missing layer are reported and left alone. It is idempotent: a rerun
// finds converted layers current. dryRun reads and verifies but writes
// nothing.
func Resign(ctx context.Context, store ResignStore, objects storage.StorageBackend, key *ecdsa.PrivateKey, dryRun bool, log *slog.Logger) (ResignReport, error) {
	if log == nil {
		log = slog.Default()
	}
	layers, err := store.StoredLayers(ctx)
	if err != nil {
		return ResignReport{}, fmt.Errorf("signd: list stored layers: %w", err)
	}
	report := ResignReport{DryRun: dryRun}
	for _, l := range layers {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		problem, err := resignOne(ctx, store, objects, key, dryRun, l, &report)
		if err != nil {
			return report, err
		}
		if problem != "" {
			log.Warn("signd: resign left a layer alone", "key", l.StorageKey, "deployment", l.DeploymentID, "reason", problem)
			report.Problems = append(report.Problems, ResignProblem{StorageKey: l.StorageKey, DeploymentID: l.DeploymentID, Reason: problem})
		}
	}
	return report, nil
}

// resignOne handles one layer. It returns why it left the layer alone, or a
// fatal error (a write or database failure) that stops the pass.
func resignOne(ctx context.Context, store ResignStore, objects storage.StorageBackend, key *ecdsa.PrivateKey,
	dryRun bool, l StoredLayer, report *ResignReport) (string, error) {
	sigKey := cosign.SigKeyFor(l.StorageKey)
	blob, err := readSignature(ctx, objects, sigKey)
	if notFound(err) {
		return "no signature", nil
	}
	if err != nil {
		return "", fmt.Errorf("signd: read %s: %w", sigKey, err)
	}
	digest, n, err := cosign.DigestObject(ctx, objects, l.StorageKey, nil)
	if notFound(err) {
		return "layer missing from the artifact store", nil
	}
	if err != nil {
		return "", fmt.Errorf("signd: hash %s: %w", l.StorageKey, err)
	}
	format, ok := cosign.VerifyArtifactSignature(&key.PublicKey, l.StorageKey, digest, blob)
	if !ok {
		return "signature does not verify under the signing key", nil
	}
	if format == "v2" {
		report.Current++
	} else {
		report.Converted++
		if !dryRun {
			doc, err := cosign.SignV2(key, l.StorageKey, digest)
			if err != nil {
				return "", fmt.Errorf("signd: sign %s: %w", l.StorageKey, err)
			}
			if err := objects.Put(ctx, sigKey, bytes.NewReader(doc)); err != nil {
				return "", fmt.Errorf("signd: write %s: %w", sigKey, err)
			}
		}
	}
	// artifact_signatures holds app and sidecar layers only; legacy
	// layers/<id>.ext4 keys get the v2 signature alone.
	if _, isApp := DeploymentOfLayerKey(l.StorageKey); !isApp || dryRun {
		return "", nil
	}
	stored, inserted, err := store.RecordSignature(ctx, Record{StorageKey: l.StorageKey, Digest: digest, Bytes: n,
		DeploymentID: strings.ToLower(l.DeploymentID), NodeName: ResignNode, Format: recordFormatV2})
	if err != nil {
		return "", fmt.Errorf("signd: record %s: %w", l.StorageKey, err)
	}
	if inserted {
		report.Recorded++
	} else if stored.Digest != digest {
		return "artifact_signatures records another digest for this key", nil
	}
	return "", nil
}

func readSignature(ctx context.Context, objects storage.StorageBackend, key string) ([]byte, error) {
	rc, err := objects.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, 4097))
}

func notFound(err error) bool {
	return err != nil && (errors.Is(err, storage.ErrNotFound) || errors.Is(err, os.ErrNotExist))
}
