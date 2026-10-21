package promotion

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type TokenWire struct {
	ReleaseSecrets        map[string]map[string]string `json:"release_secrets,omitempty"`
	Workers               bool                         `json:"workers,omitempty"`
	Migrate               bool                         `json:"migrate,omitempty"`
	Verify                bool                         `json:"verify,omitempty"`
	Version               int                          `json:"v"`
	AccountID             string                       `json:"account_id"`
	ProjectID             string                       `json:"project_id"`
	ProjectSlug           string                       `json:"project_slug"`
	FromEnvironment       string                       `json:"from_environment"`
	ToEnvironment         string                       `json:"to_environment"`
	FromConfigHash        string                       `json:"from_config_hash"`
	ToConfigHash          string                       `json:"to_config_hash"`
	FromReleaseSetID      string                       `json:"from_release_set_id,omitempty"`
	ToReleaseSetID        string                       `json:"to_release_set_id,omitempty"`
	SourceQualificationID string                       `json:"source_qualification_id,omitempty"`
	SyncConfig            bool                         `json:"sync_config,omitempty"`
	PromotionHash         string                       `json:"promotion_hash"`
	IssuedAt              int64                        `json:"issued_at"`
}

func DecodeToken(encoded string) (TokenWire, error) {
	var wire TokenWire
	if strings.TrimSpace(encoded) == "" {
		return wire, errors.New("empty promotion token")
	}
	var raw []byte
	var err error
	for _, encoding := range []*base64.Encoding{
		base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding,
	} {
		raw, err = encoding.DecodeString(encoded)
		if err == nil {
			break
		}
	}
	if err != nil {
		return wire, fmt.Errorf("promotion token base64: %w", err)
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return wire, fmt.Errorf("promotion token json: %w", err)
	}
	if wire.Version != 1 || wire.AccountID == "" || wire.ProjectID == "" || wire.ProjectSlug == "" ||
		wire.FromEnvironment == "" || wire.ToEnvironment == "" || wire.PromotionHash == "" {
		return wire, errors.New("promotion token is incomplete")
	}
	return wire, nil
}
