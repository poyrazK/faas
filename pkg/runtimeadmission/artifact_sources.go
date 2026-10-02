package runtimeadmission

// adr: 431. Source identities remain distinct from consumer acknowledgments.

import (
	"path"
	"strings"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

type ArtifactSource struct {
	Kind         string `json:"kind"`
	WorkloadName string `json:"workload_name"`
	StorageKey   string `json:"storage_key"`
	Digest       string `json:"digest"`
	Bytes        int64  `json:"bytes"`
}

func (a ArtifactSource) Valid() bool {
	if a.Bytes <= 0 || a.Bytes > api.ApplicationStandardBaseMaxArtifactBytes || ociref.ValidateDigest(a.Digest) != nil || !validArtifactSourceKey(a.StorageKey) {
		return false
	}
	switch a.Kind {
	case "base-image", "app-layer", "full-rootfs":
		return a.WorkloadName == ""
	case "sidecar-layer":
		return api.ValidSidecarName(a.WorkloadName)
	default:
		return false
	}
}

func validArtifactSourceKey(key string) bool {
	return key != "" && key != "." && len(key) <= api.ApplicationStandardBaseMaxStorageKeyBytes && !path.IsAbs(key) && path.Clean(key) == key && !strings.Contains(key, "..") && !strings.ContainsAny(key, "\\\x00\r\n")
}

// CheckArtifactSources requires exactly one approved source for every drive.
// A runtime-default base without producer evidence receives no exemption.
func CheckArtifactSources(sources []ArtifactSource, baseKey, mainKey string, sidecarKeys map[string]string) error {
	if len(sources) != len(sidecarKeys)+2 || len(sidecarKeys) > api.SidecarCapMax {
		return ErrInvalid
	}
	keys := map[string]string{"base": baseKey, "main": mainKey}
	for name, key := range sidecarKeys {
		if !api.ValidSidecarName(name) {
			return ErrInvalid
		}
		keys["sidecar:"+name] = key
	}
	seenKeys := map[string]bool{}
	for _, source := range sources {
		role := source.Role()
		if !source.Valid() || keys[role] != source.StorageKey || seenKeys[source.StorageKey] {
			return ErrInvalid
		}
		delete(keys, role)
		seenKeys[source.StorageKey] = true
	}
	if len(keys) != 0 {
		return ErrInvalid
	}
	return nil
}

func (a ArtifactSource) Role() string {
	if a.Kind == "base-image" {
		return "base"
	}
	if a.Kind == "sidecar-layer" {
		return "sidecar:" + a.WorkloadName
	}
	return "main"
}

func (a ArtifactSource) ToProto() *vmmdpb.RuntimeArtifactSource {
	return &vmmdpb.RuntimeArtifactSource{Kind: a.Kind, WorkloadName: a.WorkloadName, StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}
}

func ArtifactSourcesFromProto(values []*vmmdpb.RuntimeArtifactSource) ([]ArtifactSource, error) {
	if len(values) > api.SidecarCapMax+2 {
		return nil, ErrInvalid
	}
	sources := make([]ArtifactSource, 0, len(values))
	for _, value := range values {
		if value == nil || RejectUnknown(value) != nil {
			return nil, ErrInvalid
		}
		source := ArtifactSource{Kind: value.Kind, WorkloadName: value.WorkloadName, StorageKey: value.StorageKey, Digest: value.Digest, Bytes: value.Bytes}
		if !source.Valid() {
			return nil, ErrInvalid
		}
		sources = append(sources, source)
	}
	return sources, nil
}
