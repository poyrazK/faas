package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type ManagedPostgresExportRequest struct {
	RequestID string `json:"request_id"`
}

type ManagedPostgresExportManifest struct {
	PostgresMajor  int                               `json:"postgres_major"`
	DatabaseBytes  int64                             `json:"database_bytes,omitempty"`
	VectorSchema   string                            `json:"vector_schema,omitempty"`
	RestoreReceipt string                            `json:"restore_receipt,omitempty"`
	CapturedAt     time.Time                         `json:"captured_at"`
	Extensions     []ManagedPostgresExtensionVersion `json:"extensions"`
	SizeBytes      int64                             `json:"size_bytes"`
	SHA256         string                            `json:"sha256"`
}

type ManagedPostgresExport struct {
	ID        string                         `json:"id"`
	State     string                         `json:"state"`
	ExpiresAt time.Time                      `json:"expires_at"`
	Manifest  *ManagedPostgresExportManifest `json:"manifest,omitempty"`
}

func postgresExportPath(org, database string) string {
	prefix := "/v1"
	if org != "" {
		prefix += "/orgs/" + url.PathEscape(org)
	}
	return prefix + "/postgres/databases/" + url.PathEscape(database) + "/exports"
}

func (c *Client) StartManagedPostgresExport(ctx context.Context, org, database string, request ManagedPostgresExportRequest) (ManagedPostgresExport, error) {
	var out ManagedPostgresExport
	err := c.do(ctx, http.MethodPost, postgresExportPath(org, database), request, &out)
	return out, err
}

func (c *Client) GetManagedPostgresExport(ctx context.Context, org, database, id string) (ManagedPostgresExport, error) {
	var out ManagedPostgresExport
	err := c.do(ctx, http.MethodGet, postgresExportPath(org, database)+"/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DownloadManagedPostgresExport(ctx context.Context, org, database, id string, size int64, dst io.Writer) error {
	if size < 5 {
		return fmt.Errorf("invalid PostgreSQL export size")
	}
	path := postgresExportPath(org, database) + "/" + url.PathEscape(id) + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept-Encoding", "identity")
	client := *c.http
	// The caller's context spans the transfer. Keep the authenticated
	// archive request on the API origin, including on unexpected redirects.
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		data, err := readBoundedResponse(resp, maxResponseBodyBytes)
		if err != nil {
			return err
		}
		return apiErrorFromResponse(resp, data)
	}
	if resp.ContentLength != size {
		return fmt.Errorf("PostgreSQL export length does not match its manifest")
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, size))
	if err != nil {
		return err
	}
	if n != size {
		return &ResponseTruncatedError{Expected: size, Received: n}
	}
	return nil
}
