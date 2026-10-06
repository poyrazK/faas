package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateExecutionOutputFiles accepts a bounded, unique list of normalized
// POSIX paths below context.output_dir. No globbing or implicit export occurs.
func ValidateExecutionOutputFiles(names []string) error {
	if len(names) > ExecutionArtifactMaxFiles {
		return fmt.Errorf("output_files exceeds the %d-file limit", ExecutionArtifactMaxFiles)
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" || len(name) > ExecutionArtifactMaxPathBytes || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 || strings.ContainsAny(name, "\x00\\:") || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("output_files must contain normalized relative POSIX paths")
		}
		if seen[name] {
			return fmt.Errorf("output_files contains a duplicate path")
		}
		seen[name] = true
	}
	return nil
}

// ValidateExecutionArtifacts checks untrusted guest metadata against the bytes.
func ValidateExecutionArtifacts(artifacts []ExecutionArtifact) error {
	if len(artifacts) > ExecutionArtifactMaxFiles {
		return fmt.Errorf("too many artifacts")
	}
	names := make([]string, len(artifacts))
	for i, artifact := range artifacts {
		names[i] = artifact.Name
		digest := sha256.Sum256(artifact.Content)
		if artifact.Content == nil || artifact.SizeBytes != len(artifact.Content) || artifact.SHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
			return fmt.Errorf("artifact size or checksum is invalid")
		}
	}
	return ValidateExecutionOutputFiles(names)
}

// ExecutionArtifactsOutputBytes counts the compact JSON array, including
// base64 expansion and metadata. An absent array costs zero for legacy runs.
func ExecutionArtifactsOutputBytes(artifacts []ExecutionArtifact) int {
	if len(artifacts) == 0 {
		return 0
	}
	encoded, _ := json.Marshal(artifacts) // closed, JSON-safe fields
	return len(encoded)
}

func CloneExecutionArtifacts(artifacts []ExecutionArtifact) []ExecutionArtifact {
	if artifacts == nil {
		return nil
	}
	cloned := append([]ExecutionArtifact{}, artifacts...)
	for i := range cloned {
		cloned[i].Content = append([]byte{}, artifacts[i].Content...)
	}
	return cloned
}

// ExecutionArtifactsMatch prevents partial exports or undeclared files from
// being accepted as a successful result, including through old transports.
func ExecutionArtifactsMatch(names []string, artifacts []ExecutionArtifact) bool {
	if len(names) != len(artifacts) {
		return false
	}
	for i, name := range names {
		if name != artifacts[i].Name {
			return false
		}
	}
	return true
}
