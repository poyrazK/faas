// Package migrationadmissionwire binds the full adoption envelope to its durable
// reservation. Tracing metadata is not adoption authority.
package migrationadmissionwire

import (
	"crypto/sha256"
	"encoding/hex"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/protobuf/proto"
)

func Digest(req *vmmdpb.AdoptMigratedInstanceRequest) (string, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(req)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
