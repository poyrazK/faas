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
func verificationSourceStorage(ctx context.Context, source receiptSQL, relations []relation) ([]string, error) {
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
func physicalVerificationID(intent string, storage []string) string {
	encoded, _ := json.Marshal(struct {
		Intent  string
		Storage []string
	}{intent, storage})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
