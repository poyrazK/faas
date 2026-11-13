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
		var keyOrder string
		if err := source.QueryRow(ctx, `SELECT c.oid,c.relfilenode,c.reltoastrelid,c.reltablespace,pg_relation_filepath(c.oid),
 coalesce((SELECT indkey::text FROM pg_index WHERE indrelid=c.oid AND indisprimary AND indisvalid AND indimmediate),'')
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1 AND c.relkind='r'`, r.Name).Scan(&oid, &filenode, &toast, &tablespace, &path, &keyOrder); err != nil {
			return nil, err
		}
		if oid != r.OID {
			return nil, ErrConflict
		}
		encoded, _ := json.Marshal(struct {
			OID, File, Toast, Space uint32
			Path, KeyOrder          string
		}{oid, filenode, toast, tablespace, path, keyOrder})
		sum := sha256.Sum256(encoded)
		result[i] = hex.EncodeToString(sum[:])
	}
	return result, nil
}

// Raw physical receipts are independent of query plans. Proof fingerprints also
// bind the query key order and stream epoch, so DDL between planner hydration
// and physical observation cannot reinterpret a persisted range cursor.
func verificationQueryStorage(actual []string, epoch string, relations []relation) ([]string, error) {
	if len(actual) != len(relations) {
		return nil, ErrConflict
	}
	proof := make([]string, len(actual))
	for i, storage := range actual {
		r := relations[i] // #nosec G602 -- Equal vector lengths are checked above; i ranges over actual.
		var keys []string
		for _, key := range primaryColumns(r) {
			keys = append(keys, key.Name)
		}
		encoded, _ := json.Marshal(struct {
			Storage, Epoch string
			Keys           []string
		}{storage, epoch, keys})
		sum := sha256.Sum256(encoded)
		proof[i] = hex.EncodeToString(sum[:])
	}
	return proof, nil
}

func verificationStorage(ctx context.Context, p Pipeline, source capacityQuery, target receiptSQL, relations []relation) ([]string, []string, error) {
	actual, err := verificationSourceStorage(ctx, source, relations)
	if err != nil {
		return nil, nil, err
	}
	w, err := loadSourceSlot(ctx, p, target)
	if err != nil {
		return nil, nil, err
	}
	epoch := ""
	if w != nil {
		epoch = w.Epoch
	}
	proof, err := verificationQueryStorage(actual, epoch, relations)
	return actual, proof, err
}
func physicalVerificationID(intent string, storage []string) string {
	// Invalidate ranges proved with a different row encoding, while retaining
	// the logical intent identity used by approvals and cancellation.
	encoded, _ := json.Marshal(struct {
		Intent, Encoding string
		Storage          []string
	}{intent, "nullable-text-v2-key-order", storage})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
