package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// DataAPIRuntime is the Data API runtime the control plane currently serves
// (ADR-20261031095954). The CLI deploys this archive instead of the runtime it
// was built with, so an old CLI release cannot redeploy an old runtime once
// it fetches from apid; a running Data API reports Version from /healthz.
type DataAPIRuntime struct {
	Version       string `json:"version"`
	ArchiveSHA256 string `json:"archive_sha256"`
	// Archive is a gzipped tar of the runtime under one data-api/ directory.
	Archive []byte `json:"archive"`
}

// GetDataAPIRuntime downloads the runtime the control plane serves for the
// app and checks the archive against its digest.
func (c *Client) GetDataAPIRuntime(ctx context.Context, slug string) (DataAPIRuntime, error) {
	var out DataAPIRuntime
	if err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/data-api/runtime", nil, &out); err != nil {
		return DataAPIRuntime{}, err
	}
	if err := out.Validate(); err != nil {
		return DataAPIRuntime{}, err
	}
	return out, nil
}

// Validate checks the response shape, size and digest.
func (r DataAPIRuntime) Validate() error {
	if len(r.Version) != 16 {
		return errors.New("data API runtime response has no valid version")
	}
	if len(r.Archive) == 0 || len(r.Archive) > DataAPIRuntimeArchiveMaxBytes {
		return fmt.Errorf("data API runtime archive is %d bytes; limit is %d", len(r.Archive), DataAPIRuntimeArchiveMaxBytes)
	}
	sum := sha256.Sum256(r.Archive)
	if hex.EncodeToString(sum[:]) != r.ArchiveSHA256 {
		return errors.New("data API runtime archive does not match its digest")
	}
	return nil
}
