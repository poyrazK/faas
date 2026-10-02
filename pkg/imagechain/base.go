package imagechain

// adr: 429

import (
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

// BaseLayoutVersion changes whenever the shared base boot layout changes.
const BaseLayoutVersion = "faas-base-layout-v3"

// BaseArtifact identifies the complete base ext4, not its staged content.
type BaseArtifact struct {
	StorageKey string `json:"storage_key"`
	Digest     string `json:"digest"`
	Bytes      int64  `json:"bytes"`
}

func (a BaseArtifact) Valid() bool {
	return ValidBaseKey(a.StorageKey) && ociref.ValidateDigest(a.Digest) == nil && a.Bytes > 0 && a.Bytes <= api.ApplicationStandardBaseMaxArtifactBytes
}

func ValidBaseKey(key string) bool {
	return len(key) <= api.ApplicationStandardBaseMaxStorageKeyBytes && strings.HasPrefix(key, "base/") && strings.HasSuffix(key, ".ext4") && path.Clean(key) == key && !strings.ContainsAny(key, "\r\n\x00\\") && !strings.Contains(strings.TrimPrefix(key, "base/"), "/") && len(strings.TrimPrefix(key, "base/")) > len(".ext4")
}

// ParentMaterialization is a private native-owner receipt for the source
// bytes actually mounted/copied. It confers no customer runtime authority.
type ParentMaterialization struct {
	Artifact  BaseArtifact `json:"artifact"`
	TargetDir string       `json:"target_dir"`
}

func (m ParentMaterialization) Valid() bool {
	return m.Artifact.Valid() && path.IsAbs(m.TargetDir) && path.Clean(m.TargetDir) == m.TargetDir && len(m.TargetDir) <= api.ApplicationStandardBaseMaxPathBytes && !strings.ContainsAny(m.TargetDir, "\r\n\x00")
}
