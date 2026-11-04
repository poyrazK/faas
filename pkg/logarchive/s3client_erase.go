package logarchive

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// listObjectsMaxBodyBytes bounds one ListObjects response. A 1,000-key page
// of archive keys is well under 1 MiB.
const listObjectsMaxBodyBytes = 4 << 20

// listBucketResult is the subset of the S3 ListObjects (V1) response the
// eraser reads. GCS's XML API answers the same shape under its own
// namespace; encoding/xml matches the local names either way.
type listBucketResult struct {
	IsTruncated bool   `xml:"IsTruncated"`
	NextMarker  string `xml:"NextMarker"`
	Contents    []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
}

// ListObjectKeys returns one page of the keys under prefix that sort after
// marker, and the marker for the next page ("" when this page is the last).
// It uses ListObjects V1, which S3 and the GCS XML API both serve.
func (c *S3Client) ListObjectKeys(ctx context.Context, prefix, marker string, maxKeys int) ([]string, string, error) {
	query := url.Values{}
	query.Set("prefix", prefix)
	if marker != "" {
		query.Set("marker", marker)
	}
	if maxKeys > 0 {
		query.Set("max-keys", strconv.Itoa(maxKeys))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.bucketURL(query), nil)
	if err != nil {
		return nil, "", fmt.Errorf("logarchive: build list request: %w", err)
	}
	if err := c.authorize(req, hexSHA256(nil)); err != nil {
		return nil, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("logarchive: list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkS3Status(resp, "LIST"); err != nil {
		return nil, "", err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, listObjectsMaxBodyBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("logarchive: read list body: %w", err)
	}
	if len(body) > listObjectsMaxBodyBytes {
		return nil, "", errors.New("logarchive: list response exceeds the size bound")
	}
	var result listBucketResult
	if err := xml.Unmarshal(body, &result); err != nil {
		return nil, "", fmt.Errorf("logarchive: decode list body: %w", err)
	}
	keys := make([]string, 0, len(result.Contents))
	for _, item := range result.Contents {
		keys = append(keys, item.Key)
	}
	if !result.IsTruncated {
		return keys, "", nil
	}
	next := result.NextMarker
	if next == "" && len(keys) > 0 {
		// S3 sends NextMarker only with a delimiter; the last key is the
		// documented marker otherwise.
		next = keys[len(keys)-1]
	}
	if next == "" || next == marker {
		return nil, "", errors.New("logarchive: truncated list response without a usable marker")
	}
	return keys, next, nil
}

// DeleteObject removes {bucket}/{key}. An object that is already gone counts
// as deleted, so the eraser can repeat a step safely.
func (c *S3Client) DeleteObject(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.objectURL(key), nil)
	if err != nil {
		return fmt.Errorf("logarchive: build delete request: %w", err)
	}
	if err := c.authorize(req, hexSHA256(nil)); err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("logarchive: delete: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	err = parseS3Response(resp, "DELETE")
	var permanent *Permanent
	if errors.As(err, &permanent) && permanent.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

// bucketURL is the path-style bucket URL with the given query.
func (c *S3Client) bucketURL(query url.Values) string {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return c.Endpoint + "/" + c.Bucket + "?" + query.Encode()
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + c.Bucket
	u.RawQuery = query.Encode()
	return u.String()
}
