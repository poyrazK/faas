package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

// ArchiveManifest selects the fields authenticated by the restore receipt.
func (m ManagedPostgresExportManifest) ArchiveManifest() pgarchive.Manifest {
	source := pgexport.Source{
		Major: m.PostgresMajor, CapturedAt: m.CapturedAt, DatabaseBytes: m.DatabaseBytes, VectorSchema: m.VectorSchema,
		Extensions: make([]pgextension.Installed, 0, len(m.Extensions)),
	}
	for _, extension := range m.Extensions {
		source.Extensions = append(source.Extensions, pgextension.Installed{Name: extension.Name, Version: extension.Version})
	}
	return pgarchive.Manifest{Source: source, SizeBytes: m.SizeBytes, SHA256: m.SHA256}
}

// UploadManagedPostgresArchive streams the manifest and archive to the
// authenticated API. Uploading consumes the reader once.
func (c *Client) UploadManagedPostgresArchive(ctx context.Context, org, database, operation string, manifest ManagedPostgresExportManifest, archive io.Reader) (ManagedPostgresImportStatus, error) {
	var result ManagedPostgresImportStatus
	if archive == nil || !manifest.ArchiveManifest().Valid() || manifest.RestoreReceipt == "" {
		return result, pgarchive.ErrArchive
	}
	var headers bytes.Buffer
	multipartBody := multipart.NewWriter(&headers)
	raw, err := json.Marshal(manifest)
	if err != nil || len(raw) > pgarchive.ManifestLimit {
		return result, pgarchive.ErrArchive
	}
	if err := multipartBody.WriteField("manifest", string(raw)); err != nil {
		return result, err
	}
	if _, err := multipartBody.CreateFormFile("archive", "postgres.dump"); err != nil {
		return result, err
	}
	bodyStart := headers.Len()
	if err := multipartBody.Close(); err != nil {
		return result, err
	}
	payload := headers.Bytes()
	body := io.MultiReader(bytes.NewReader(payload[:bodyStart]), archive, bytes.NewReader(payload[bodyStart:]))
	prefix := "/v1"
	if org != "" {
		prefix += "/orgs/" + url.PathEscape(org)
	}
	path := prefix + "/postgres/databases/" + url.PathEscape(database) + "/imports/" + url.PathEscape(operation) + "/archive"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return result, err
	}
	req.ContentLength = int64(len(payload)) + manifest.SizeBytes
	req.Header.Set("Content-Type", multipartBody.FormDataContentType())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	client := *c.http
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close() //nolint:errcheck
	data, err := readBoundedResponse(resp, maxResponseBodyBytes)
	if err != nil {
		return result, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return result, apiErrorFromResponse(resp, data)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode PostgreSQL import progress: %w", err)
	}
	return result, nil
}
