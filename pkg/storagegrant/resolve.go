// Package storagegrant decides which shared-store objects a compute node may
// reach and signs per-object URLs for them (ADR-20261117190000).
//
// Resolve is the only parser of the artifact keyspace for this purpose: it
// maps a key to the subject that owns it, and secretd admits a grant only
// when that subject is placed on the calling node. A key it does not know is
// refused, so a new key class must be added here before it ships.
package storagegrant

// adr: 20261117190000

import (
	"strings"
)

// Class is the kind of subject that owns a key.
type Class string

const (
	// ClassPlatform keys (base images, kernels, agent and sandbox layers,
	// the shared sandbox preset snapshot, base scan sidecars) are readable
	// by every node; a node may create one or replace the one it read,
	// conditioned on its ETag or generation (ADR amendment 1: compute
	// imaged stages and patches bases, and their integrity rests on the
	// control plane's signature, not on who wrote them).
	ClassPlatform Class = "platform"
	// ClassDeployment keys belong to one deployment of one app.
	ClassDeployment Class = "deployment"
	// ClassSandboxAccount keys belong to one account's sandbox sessions.
	ClassSandboxAccount Class = "sandbox_account"
	// ClassJob keys belong to one job.
	ClassJob Class = "job"
	// ClassBuild keys (a build's uploaded source) belong to one build.
	ClassBuild Class = "build"
	// ClassBuildOutput keys (a build's SBOM) are created by the node
	// running that build.
	ClassBuildOutput Class = "build_output"
	// ClassUnknown keys are refused.
	ClassUnknown Class = "unknown"
)

// Subject is what owns a key. ID is a deployment, account or job ID;
// it is empty for platform keys.
type Subject struct {
	Class Class
	ID    string
}

// sealedSuffix is the twin a sealed snapshot object is stored under.
const sealedSuffix = ".age"

// Resolve maps key to its owning subject. It never touches the store or
// the database, and it refuses anything not in the ADR's table: path
// traversal, empty segments, non-canonical IDs and unknown prefixes.
func Resolve(key string) Subject {
	unknown := Subject{Class: ClassUnknown}
	if key == "" || len(key) > 1024 || strings.HasPrefix(key, "/") || strings.Contains(key, "//") ||
		strings.Contains(key, "..") || strings.ContainsAny(key, "\\\x00?#") {
		return unknown
	}
	top, rest, ok := strings.Cut(key, "/")
	if !ok || rest == "" {
		return unknown
	}
	switch top {
	case "base", "kernel":
		return Subject{Class: ClassPlatform}
	case "sigs":
		// sigs/<key>.sig is the signature of <key> and belongs to its
		// owner (pkg/cosign SignatureKey).
		if inner, ok := strings.CutSuffix(rest, ".sig"); ok && !strings.HasPrefix(inner, "sigs/") {
			return Resolve(inner)
		}
	case "scans":
		// Grype sidecars of platform bases (pkg/wire BaseScanKeyForArch,
		// ScanKeyForBaseKey): scans/<base name>.scan.json, or
		// scans/generations/<sha256>/<base name>.scan.json for a
		// generation-addressed base (storagekey.BaseGeneration).
		if name, ok := strings.CutSuffix(rest, ".scan.json"); ok && baseScanName(name) {
			return Subject{Class: ClassPlatform}
		}
	case "sboms":
		if build, ok := strings.CutSuffix(rest, ".cdx.json"); ok && canonicalUUID(build) {
			return Subject{Class: ClassBuildOutput, ID: build}
		}
	case "snap":
		return resolveSnapshot(rest)
	case "apps":
		return resolveAppLayer(rest)
	case "layers":
		if dep, ok := strings.CutSuffix(rest, ".ext4"); ok && canonicalUUID(dep) {
			return Subject{Class: ClassDeployment, ID: dep}
		}
	case "jobs":
		name, ok := strings.CutSuffix(rest, ".ext4")
		if !ok {
			return unknown
		}
		job, artifact, attempt := strings.Cut(name, "__")
		if canonicalUUID(job) && (!attempt || canonicalUUID(artifact)) {
			return Subject{Class: ClassJob, ID: job}
		}
	case "sources":
		if build, ok := strings.CutSuffix(rest, ".tar.gz"); ok && canonicalUUID(build) {
			return Subject{Class: ClassBuild, ID: build}
		}
	case "sandbox-screenshots":
		parts := strings.Split(rest, "/")
		if len(parts) == 3 && canonicalUUID(parts[0]) && canonicalUUID(parts[1]) {
			if action, ok := strings.CutSuffix(parts[2], ".jpg"); ok && canonicalUUID(action) {
				return Subject{Class: ClassSandboxAccount, ID: parts[0]}
			}
		}
	}
	return unknown
}

// resolveSnapshot handles snap/<namespace>/<object...>.
func resolveSnapshot(rest string) Subject {
	unknown := Subject{Class: ClassUnknown}
	namespace, object, ok := strings.Cut(rest, "/")
	if !ok || !snapshotObject(object) {
		return unknown
	}
	switch {
	case canonicalUUID(namespace):
		return Subject{Class: ClassDeployment, ID: namespace}
	case namespace == "sandbox-preset":
		return Subject{Class: ClassPlatform}
	}
	if account, ok := strings.CutPrefix(namespace, "sandbox-template-"); ok && canonicalUUID(account) {
		return Subject{Class: ClassSandboxAccount, ID: account}
	}
	return unknown
}

// snapshotObject reports whether object is one of a snapshot namespace's
// object shapes: <name>, warm/<name>, captures/<id>/v2/<name> or
// warm/captures/<id>/v2/<name>, each optionally with the sealed twin suffix.
func snapshotObject(object string) bool {
	object = strings.TrimSuffix(object, sealedSuffix)
	object = strings.TrimPrefix(object, "warm/")
	parts := strings.Split(object, "/")
	switch len(parts) {
	case 1:
		return plainSegment(parts[0])
	case 4:
		return parts[0] == "captures" && plainSegment(parts[1]) && parts[2] == "v2" && plainSegment(parts[3])
	}
	return false
}

// resolveAppLayer handles apps/<slug>/<deployment>[-<sidecar>].ext4.
func resolveAppLayer(rest string) Subject {
	unknown := Subject{Class: ClassUnknown}
	slug, file, ok := strings.Cut(rest, "/")
	if !ok || !plainSegment(slug) || strings.Contains(file, "/") {
		return unknown
	}
	name, ok := strings.CutSuffix(file, ".ext4")
	if !ok || len(name) < 36 {
		return unknown
	}
	dep, sidecar := name[:36], name[36:]
	if !canonicalUUID(dep) {
		return unknown
	}
	if sidecar != "" {
		if s, ok := strings.CutPrefix(sidecar, "-"); !ok || !plainSegment(s) {
			return unknown
		}
	}
	return Subject{Class: ClassDeployment, ID: dep}
}

// plainSegment is a non-empty path segment of safe characters.
func plainSegment(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for _, c := range s {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// canonicalUUID reports whether s is a lowercase hyphenated UUID.
func canonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// baseScanName reports whether name, from scans/<name>.scan.json, names a
// platform base: a bare base file name, or generations/<sha256 hex>/<bare
// base file name>.
func baseScanName(name string) bool {
	if gen, ok := strings.CutPrefix(name, "generations/"); ok {
		sum, base, ok := strings.Cut(gen, "/")
		if !ok || len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" {
			return false
		}
		name = base
	}
	return name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
}
