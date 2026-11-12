package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var errVerificationSourceRewritten = errors.New("verification source storage changed")

// Rewrites can change values without producing logical row events. Fence the
// proof with physical storage identity as well as the logical schema. These
// opaque fingerprints never expose server paths or row keys to the catalog.
func verificationSourceStorage(ctx context.Context, source capacityQuery, relations []relation) ([]string, error) {
	result := make([]string, len(relations))
	for i, r := range relations {
		var oid, filenode, toast, tablespace uint32
		var path string
		if err := source.QueryRow(ctx, `SELECT c.oid,c.relfilenode,c.reltoastrelid,c.reltablespace,pg_relation_filepath(c.oid) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1 AND c.relkind='r'`, r.Name).Scan(&oid, &filenode, &toast, &tablespace, &path); err != nil {
			return nil, err
		}
		if oid != r.OID {
			return nil, ErrConflict
		}
		encoded, _ := json.Marshal(struct {
			OID, File, Toast, Space uint32
			Path                    string
		}{oid, filenode, toast, tablespace, path})
		sum := sha256.Sum256(encoded)
		result[i] = hex.EncodeToString(sum[:])
	}
	return result, nil
}

// A proof from before an uncertain stream transition cannot qualify the new
// stream, even when every source table still has identical physical storage.
// Keep raw storage receipts separate so later maintenance comparisons retain
// their original meaning. An empty epoch preserves existing proof identities.
func verificationStorage(ctx context.Context, p Pipeline, source capacityQuery, target receiptSQL, relations []relation) ([]string, []string, error) {
	actual, err := verificationSourceStorage(ctx, source, relations)
	if err != nil {
		return nil, nil, err
	}
	w, err := loadSourceSlot(ctx, p, target)
	if err != nil {
		return nil, nil, err
	}
	if w == nil || w.Epoch == "" {
		return actual, actual, nil
	}
	proof := make([]string, len(actual))
	for i, storage := range actual {
		sum := sha256.Sum256([]byte(storage + ":" + w.Epoch))
		proof[i] = hex.EncodeToString(sum[:])
	}
	return actual, proof, nil
}
func physicalVerificationID(intent string, storage []string) string {
	// Invalidate ranges proved with a different row encoding, while retaining
	// the logical intent identity used by approvals and cancellation.
	encoded, _ := json.Marshal(struct {
		Intent, Encoding string
		Storage          []string
	}{intent, "nullable-text-v1", storage})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
