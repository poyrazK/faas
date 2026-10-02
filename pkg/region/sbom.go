package region

import (
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// BuildSBOMKey binds build metadata to the same region as its runtime artifact.
func BuildSBOMKey(id, buildID string) string {
	return ArtifactKey(id, "sboms/"+buildID+".cdx.json")
}

// ValidBuildSBOMKey rejects aliases and references to another build or region.
// Older builds used lower-case 32-hex IDs; newer builds use canonical UUIDs.
func ValidBuildSBOMKey(id, buildID, key string) bool {
	if !Valid(id) || key != BuildSBOMKey(id, buildID) {
		return false
	}
	if u, err := uuid.Parse(buildID); err == nil && u.String() == buildID {
		return true
	}
	if len(buildID) == 32 && strings.ToLower(buildID) == buildID {
		_, err := hex.DecodeString(buildID)
		return err == nil
	}
	return false
}
