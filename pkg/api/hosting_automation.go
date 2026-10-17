package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const HostingAutomationTokenPrefix = "fp_host_"

func GenerateHostingAutomationToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := HostingAutomationTokenPrefix + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}
func ValidHostingAutomationToken(token string) bool {
	if !strings.HasPrefix(token, HostingAutomationTokenPrefix) || len(token) != len(HostingAutomationTokenPrefix)+64 {
		return false
	}
	for _, c := range token[len(HostingAutomationTokenPrefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

type HostingAutomationCredential struct {
	OIDCTrustID string    `json:"oidc_trust_id,omitempty"`
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id"`
	SiteID      string    `json:"site_id"`
	Label       string    `json:"label"`
	Operations  []string  `json:"operations"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Hash        []byte    `json:"-"`
}
type CreateHostingAutomationCredentialRequest struct {
	Label      string   `json:"label"`
	Operations []string `json:"operations,omitempty"`
	TTLSeconds int64    `json:"ttl_seconds,omitempty"`
}
type HostingAutomationCredentialResponse struct {
	Credential HostingAutomationCredential `json:"credential"`
	Token      string                      `json:"token"`
}
type HostingCIRunRequest struct {
	Manifest          HostingManifest           `json:"manifest"`
	Provenance        *HostingReleaseProvenance `json:"provenance,omitempty"`
	PreviewTTLSeconds int64                     `json:"preview_ttl_seconds"`
	ReservePreview    bool                      `json:"reserve_preview,omitempty"`
}

// Run identities survive release/preview GC. Reusing one never allocates new IDs.
type HostingCIRun struct {
	ID               string    `json:"id"`
	SiteID           string    `json:"site_id"`
	ReleaseID        string    `json:"release_id"`
	PreviewID        string    `json:"preview_id"`
	Canceled         bool      `json:"canceled,omitempty"`
	PreviewCreated   bool      `json:"preview_created"`
	PreviewReserved  bool      `json:"preview_reserved,omitempty"`
	PreviewExpiresAt time.Time `json:"preview_expires_at"`
	Fingerprint      string    `json:"fingerprint"`
	CreatedAt        time.Time `json:"created_at"`
}
type HostingCIResult struct {
	Run        HostingCIRun `json:"run"`
	State      string       `json:"state"`
	PreviewURL string       `json:"preview_url,omitempty"`
	Published  bool         `json:"published"`
}
