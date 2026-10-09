package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// These private receipts contain original identities, never credentials or
// worker tokens. Their canonical JSON encoding is the checkpoint proof.
type ProjectEnvironmentCloneCheckpointSourceReceipt struct {
	Kind, ID, CaptureIdentity string
}

type ProjectEnvironmentCloneCheckpointReceipt struct {
	RequestID, PlanSHA256, CheckpointID string
	CapturedAt                          time.Time
	CompleteWriterCoverage              bool
	Sources                             []ProjectEnvironmentCloneCheckpointSourceReceipt
}

type ProjectEnvironmentCloneCheckpointSnapshot struct {
	SourceDatabaseID string
	RequestStartedAt time.Time
	Observation      ProjectEnvironmentClonePostgresSnapshotObservation
}

type ProjectEnvironmentCloneCheckpointManifest struct {
	SourceBucketID, Hash string
	CapturedAt           time.Time
	Objects              []ProjectEnvironmentCloneObjectVersion
	// Independently observed retention at the original placement. This is
	// deliberately outside the immutable content hash and adoption digest.
	RetainedUntil time.Time
}

type ProjectEnvironmentCloneCheckpointAdoption struct {
	Receipt   ProjectEnvironmentCloneCheckpointReceipt
	Snapshots []ProjectEnvironmentCloneCheckpointSnapshot
	Manifests []ProjectEnvironmentCloneCheckpointManifest
}

type ProjectEnvironmentCloneCheckpointAdoptionStore interface {
	AdoptProjectEnvironmentCloneCheckpoint(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointAdoption, int) (ProjectEnvironmentCloneOperation, error)
}

func cloneCheckpointSHA(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func canonicalCloneCheckpointAdoption(in ProjectEnvironmentCloneCheckpointAdoption) ProjectEnvironmentCloneCheckpointAdoption {
	in.Receipt.Sources = append([]ProjectEnvironmentCloneCheckpointSourceReceipt(nil), in.Receipt.Sources...)
	in.Snapshots = append([]ProjectEnvironmentCloneCheckpointSnapshot(nil), in.Snapshots...)
	in.Manifests = append([]ProjectEnvironmentCloneCheckpointManifest(nil), in.Manifests...)
	for i := range in.Manifests {
		in.Manifests[i].Objects = append(in.Manifests[i].Objects[:0:0], in.Manifests[i].Objects...)
	}
	sort.Slice(in.Snapshots, func(i, j int) bool { return in.Snapshots[i].SourceDatabaseID < in.Snapshots[j].SourceDatabaseID })
	sort.Slice(in.Manifests, func(i, j int) bool { return in.Manifests[i].SourceBucketID < in.Manifests[j].SourceBucketID })
	return in
}

func cloneCheckpointAdoptionSHA(in ProjectEnvironmentCloneCheckpointAdoption) string {
	in = canonicalCloneCheckpointAdoption(in)
	for i := range in.Manifests {
		in.Manifests[i].RetainedUntil = time.Time{}
	}
	return cloneCheckpointSHA(in)
}

func validateCloneCheckpointAdoptionReceipt(op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointAdoption) error {
	if op.Status != CloneOperationCapturing {
		return ErrConflict
	}
	return validateCloneCheckpointAdoptionReceiptContents(op, in)
}

// Immutable receipt authentication is also needed during compensation. This
// helper grants no adoption authority; writers retain the capturing-state fence.
func validateCloneCheckpointAdoptionReceiptContents(op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointAdoption) error {
	if len(op.Resources) < 2 {
		return ErrConflict
	}
	intent, proof := op.Resources[0], op.Resources[1]
	r := in.Receipt
	if intent.Kind != "data_checkpoint" || intent.Name != op.SourceEnvironment || intent.SourceID != op.ID || intent.Status != "captured" ||
		intent.TargetID != r.CheckpointID || intent.SourceVersion != r.PlanSHA256 || intent.CapturePoint != r.CapturedAt.UTC().Format(time.RFC3339Nano) ||
		proof.Kind != "data_checkpoint_receipt" || proof.Name != intent.Name || proof.SourceID != r.CheckpointID || proof.SourceVersion != cloneCheckpointSHA(r) ||
		proof.TargetID != "" || proof.Status != "captured" || proof.CapturePoint != intent.CapturePoint ||
		r.RequestID != op.ID || !r.CompleteWriterCoverage || !validCloneObjectSHA256(r.PlanSHA256) || r.CapturedAt.IsZero() ||
		r.CapturedAt.Location() != time.UTC || r.CapturedAt.Nanosecond()%1000 != 0 || r.CapturedAt.After(time.Now()) || len(r.Sources) == 0 {
		return ErrConflict
	}
	return nil
}

// Validate all observations before any reservation or journal write. Source
// configuration comes from the authenticated frozen catalogue, not the caller.
func validateCloneCheckpointAdoptionSources(in ProjectEnvironmentCloneCheckpointAdoption,
	views []ProjectEnvironmentCloneBindings, retainUntil time.Time) (map[string]ProjectEnvironmentClonePostgresBinding, map[string]string, error) {
	databases := map[string]ProjectEnvironmentClonePostgresBinding{}
	buckets := map[string]string{}
	for _, view := range views {
		for _, db := range view.Postgres {
			if !validClonePostgresBinding(db) || db.DataResourceID == "" {
				return nil, nil, ErrConflict
			}
			if previous, ok := databases[db.DatabaseID]; ok && !sameClonePostgresDatabase(previous, db) {
				return nil, nil, ErrConflict
			}
			databases[db.DatabaseID] = db
		}
		for _, bucket := range view.Buckets {
			if _, duplicate := buckets[bucket.ID]; duplicate {
				return nil, nil, ErrConflict
			}
			buckets[bucket.ID] = view.AppID
		}
	}
	if len(in.Snapshots) != len(databases) || len(in.Manifests) != len(buckets) || len(in.Receipt.Sources) != len(databases)+len(buckets) {
		return nil, nil, ErrConflict
	}
	identities := map[string]string{}
	previous := ""
	for _, source := range in.Receipt.Sources {
		key := source.Kind + "\x00" + source.ID
		if key <= previous || source.CaptureIdentity == "" {
			return nil, nil, ErrConflict
		}
		switch source.Kind {
		case "managed_postgres":
			if _, ok := databases[source.ID]; !ok {
				return nil, nil, ErrConflict
			}
		case "object_storage":
			if _, ok := buckets[source.ID]; !ok {
				return nil, nil, ErrConflict
			}
		default:
			return nil, nil, ErrConflict
		}
		identities[key], previous = source.CaptureIdentity, key
	}
	seen := map[string]bool{}
	for _, snapshot := range in.Snapshots {
		db, ok := databases[snapshot.SourceDatabaseID]
		actual := snapshot.Observation
		expected := ProjectEnvironmentClonePostgresSnapshot{SourceDataResourceID: db.DataResourceID, CapturePoint: in.Receipt.CapturedAt}
		if !ok || seen[snapshot.SourceDatabaseID] || actual.ProviderSnapshotID != identities["managed_postgres\x00"+snapshot.SourceDatabaseID] ||
			actual.CreatedAt.After(time.Now()) || actual.CreatedAt.Location() != time.UTC || validateCloneSnapshotObservation(expected, actual) != nil ||
			snapshot.RequestStartedAt.IsZero() || snapshot.RequestStartedAt.Location() != time.UTC || snapshot.RequestStartedAt.Nanosecond()%1000 != 0 || snapshot.RequestStartedAt.After(actual.CreatedAt) {
			return nil, nil, ErrConflict
		}
		seen[snapshot.SourceDatabaseID] = true
	}
	seen = map[string]bool{}
	for _, manifest := range in.Manifests {
		_, ok := buckets[manifest.SourceBucketID]
		hash, err := ProjectEnvironmentCloneObjectManifestHash(manifest.Objects)
		if !ok || seen[manifest.SourceBucketID] || err != nil || hash != manifest.Hash || hash != identities["object_storage\x00"+manifest.SourceBucketID] ||
			!manifest.CapturedAt.Equal(in.Receipt.CapturedAt) || manifest.RetainedUntil.Before(retainUntil) {
			return nil, nil, ErrConflict
		}
		for _, item := range manifest.Objects {
			if item.Size < 0 || item.LastModified.IsZero() || item.LastModified.After(manifest.CapturedAt) ||
				!item.ValidUntil.IsZero() && !item.ValidUntil.After(manifest.CapturedAt) {
				return nil, nil, ErrConflict
			}
		}
		seen[manifest.SourceBucketID] = true
	}
	return databases, buckets, nil
}

func cloneHasCommonCheckpoint(op ProjectEnvironmentCloneOperation) bool {
	for _, resource := range op.Resources {
		if resource.Kind == "data_checkpoint" {
			return true
		}
	}
	return false
}
