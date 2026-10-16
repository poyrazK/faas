// Package hosting implements the opt-in internal static-site artifact contract.
package hosting

import (
	"encoding/hex"
	"errors"
	"mime"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrInvalidArtifact = errors.New("hosting artifact does not match its manifest")

func ValidDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 32 && strings.ToLower(value) == value
}

// ValidPath accepts canonical, relative URL paths. Percent escapes are excluded
// so the CLI, object store, CDN, and browser cannot interpret different paths.
func ValidPath(name string) bool {
	if name == "" || len(name) > api.HostingPathMaxBytes || !utf8.ValidString(name) || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~/", c) {
			continue
		}
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return false
		}
	}
	return name != ReleaseConfigFilename && !strings.HasPrefix(name, "_gregale/")
}

func ValidateManifest(m api.HostingManifest) error {
	if len(m.Files) == 0 || len(m.Files) > api.HostingFilesMax {
		return errors.New("hosting manifest has too many files or is empty")
	}
	seen := make(map[string]bool, len(m.Files))
	var total int64
	for _, f := range m.Files {
		digest, err := hex.DecodeString(f.SHA256)
		_, _, mimeErr := mime.ParseMediaType(f.ContentType)
		if !ValidPath(f.Path) || seen[f.Path] || f.Size < 0 || f.Size > api.HostingFileMaxBytes || err != nil || len(digest) != 32 || strings.ToLower(f.SHA256) != f.SHA256 || mimeErr != nil || len(f.ContentType) > api.HostingContentTypeMaxBytes {
			return errors.New("hosting files require unique canonical paths, valid SHA-256 digests, sizes, and content types")
		}
		total += f.Size
		if total > api.HostingReleaseMaxBytes {
			return errors.New("hosting release exceeds its byte limit")
		}
		seen[f.Path] = true
	}
	if !seen["index.html"] {
		return errors.New("hosting release requires index.html")
	}
	return ValidateReleaseConfig(m.Config, m.Files)
}

func ValidName(name string) bool {
	if len(name) == 0 || len(name) > api.HostingNameMaxBytes || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func ReleasePrefix(siteID, releaseID string) string {
	return "hosting/published/" + siteID + "/" + releaseID + "/"
}

func stagingKey(siteID, releaseID, name string) string {
	return "hosting/staging/" + siteID + "/" + releaseID + "/" + name
}
