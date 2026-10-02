package region

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const BuildExportPrefix = "build-exports/"

// BuildExportKey gives every publication its own UUIDv7 retention clock. The
// digest also prevents a stale build attempt from replacing committed bytes.
func BuildExportKey(id, publicationID, digest string) string {
	return ArtifactKey(id, BuildExportPrefix+publicationID+"/"+digest+".oci.tar")
}

func ParseBuildExportKey(key string) (id, publicationID, digest string, err error) {
	id, relative, err := SplitArtifactKey(key)
	if err != nil {
		return "", "", "", err
	}
	parts := strings.Split(relative, "/")
	if len(parts) != 3 || parts[0]+"/" != BuildExportPrefix || !strings.HasSuffix(parts[2], ".oci.tar") {
		return "", "", "", fmt.Errorf("invalid build export key")
	}
	u, err := uuid.Parse(parts[1])
	if err != nil || u.Version() != 7 || u.Variant() != uuid.RFC4122 || u.String() != parts[1] {
		return "", "", "", fmt.Errorf("build export publication requires a canonical UUIDv7")
	}
	digest = strings.TrimSuffix(parts[2], ".oci.tar")
	if !ValidArtifactSHA256(digest) {
		return "", "", "", fmt.Errorf("invalid build export digest")
	}
	return id, parts[1], digest, nil
}

func ValidArtifactSHA256(digest string) bool {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
