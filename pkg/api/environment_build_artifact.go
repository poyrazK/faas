package api

import (
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// ValidEnvironmentBuildArtifactReference validates shape only, never trust.
func ValidEnvironmentBuildArtifactReference(id, hash string) bool {
	parsed, err := uuid.Parse(id)
	decoded, digestErr := hex.DecodeString(hash)
	return err == nil && parsed != uuid.Nil && parsed.String() == id && digestErr == nil && len(decoded) == 32 && strings.ToLower(hash) == hash
}
