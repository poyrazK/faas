package hosting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func ValidHostingAutomationOperations(ops []string) bool {
	if len(ops) < 1 || len(ops) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if op != "stage" && op != "preview" || seen[op] {
			return false
		}
		seen[op] = true
	}
	return true
}
func ValidHostingAutomationLabel(s string) bool {
	if s == "" || len(s) > api.HostingAutomationLabelMaxBytes || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
func CIFingerprint(req api.HostingCIRunRequest) (string, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func ValidCIRequest(req api.HostingCIRunRequest) bool {
	data, err := json.Marshal(req.Manifest)
	return err == nil && int64(len(data)) <= api.HostingManifestMaxBytes && ValidateManifest(req.Manifest) == nil && ValidateProvenance(req.Provenance) == nil && req.PreviewTTLSeconds >= int64(api.HostingPreviewTTLMin/time.Second) && req.PreviewTTLSeconds <= int64(api.HostingPreviewTTLMax/time.Second)
}

func ValidCIRunID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
