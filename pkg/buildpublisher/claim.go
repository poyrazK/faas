package buildpublisher

// adr: 435. A build publisher is explicitly approved; platform identity is not trust.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrInvalid = errors.New("build publisher evidence invalid")

const Format = "gregale.build-export.v1"

// Claims contains no source URL, environment, local path or key material.
// The complete local OCI archive is the subject; this is not a registry claim.
type Claims struct {
	Format         string `json:"format"`
	AccountID      string `json:"account_id"`
	OrgID          string `json:"org_id"`
	AppID          string `json:"app_id"`
	DeploymentID   string `json:"deployment_id"`
	BuildID        string `json:"build_id"`
	ClaimStartedAt string `json:"claim_started_at"`
	SourceSHA256   string `json:"source_sha256"`
	ExportDigest   string `json:"export_digest"`
	ExportBytes    int64  `json:"export_bytes"`
	Runtime        string `json:"runtime"`
	BuilderNodeID  string `json:"builder_node_id"`
}

func Payload(in Claims) ([]byte, error) {
	for _, id := range []string{in.AccountID, in.AppID, in.DeploymentID, in.BuildID} {
		if !canonicalID(id) {
			return nil, ErrInvalid
		}
	}
	if in.OrgID != "" && !canonicalID(in.OrgID) {
		return nil, ErrInvalid
	}
	started, err := time.Parse(time.RFC3339Nano, in.ClaimStartedAt)
	if err != nil || started.IsZero() || started.UTC().Format(time.RFC3339Nano) != in.ClaimStartedAt ||
		in.Format != Format || !validHash(in.SourceSHA256) || !strings.HasPrefix(in.ExportDigest, "sha256:") || !validHash(strings.TrimPrefix(in.ExportDigest, "sha256:")) ||
		in.ExportBytes <= 0 || in.ExportBytes > api.LocalOCIMaxArchiveBytes || !boundedLabel(in.Runtime, true) || !boundedLabel(in.BuilderNodeID, true) {
		return nil, ErrInvalid
	}
	payload, err := json.Marshal(in)
	if err != nil || len(payload) > api.BuildExportMaxPublicationPayloadBytes {
		return nil, ErrInvalid
	}
	return payload, nil
}

func canonicalID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u != uuid.Nil && u.String() == s
}

func validHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func boundedLabel(s string, empty bool) bool {
	return (empty || s != "") && utf8.ValidString(s) && strings.TrimSpace(s) == s && len(s) <= api.ApplicationStandardMaxResourceNameBytes && !strings.ContainsAny(s, "\r\n\x00")
}
