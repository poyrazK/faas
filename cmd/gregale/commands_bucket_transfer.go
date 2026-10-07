package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const bucketTransferUsage = "usage: gregale bucket upload <app> <bucket-id> <key> <file> [--content-type TYPE] [--timeout DURATION] | gregale bucket download <app> <bucket-id> <key> <file> [--force] [--timeout DURATION]"

type bucketTransferOptions struct {
	action, app, bucket, key, path, contentType string
	timeout                                     time.Duration
	force                                       bool
}
type bucketTransferResult struct {
	Key      string `json:"key"`
	Path     string `json:"path"`
	Bytes    int64  `json:"size_bytes"`
	UploadID string `json:"upload_id,omitempty"`
	ETag     string `json:"etag,omitempty"`
	Status   string `json:"status"`
}

type bucketTransferClient interface {
	ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error)
	SignBucketObject(context.Context, string, string, api.ObjectSignRequest) (api.ObjectSignedRequest, error)
	CreateObjectMultipartUpload(context.Context, string, string, api.CreateObjectMultipartUploadRequest) (api.ObjectMultipartUpload, error)
	SignObjectMultipartPart(context.Context, string, string, string, int, api.ObjectMultipartPartSignRequest) (api.ObjectSignedRequest, error)
	CompleteObjectMultipartUpload(context.Context, string, string, string, api.CompleteObjectMultipartUploadRequest) (api.ObjectMultipartUpload, error)
}

func parseBucketTransfer(args []string) (bucketTransferOptions, error) {
	o := bucketTransferOptions{}
	if len(args) < 5 || args[0] != "upload" && args[0] != "download" || !api.ValidAppSlug(args[1]) {
		return o, errors.New("invalid transfer command")
	}
	o.action, o.app, o.bucket, o.key, o.path = args[0], args[1], args[2], args[3], args[4]
	if id, err := uuid.Parse(o.bucket); err != nil || id == uuid.Nil || id.String() != o.bucket || len(o.key) == 0 || len(o.key) > api.MaxObjectS3ListTextBytes || !utf8.ValidString(o.key) || strings.ContainsAny(o.key, "\x00\r\n") || o.path == "" || o.path == "-" {
		return o, errors.New("invalid bucket, key or file")
	}
	fs := newFlagSet("bucket "+o.action, flag.ContinueOnError)
	setFlagOutput(fs, osStderr)
	fs.DurationVar(&o.timeout, "timeout", api.ObjectTransferTimeout, "transfer deadline")
	if o.action == "upload" {
		fs.StringVar(&o.contentType, "content-type", "application/octet-stream", "object content type")
	} else {
		fs.BoolVar(&o.force, "force", false, "replace destination after a complete download")
	}
	if err := fs.Parse(args[5:]); err != nil {
		return o, err
	}
	if fs.NArg() != 0 || o.timeout <= 0 || o.timeout > api.MaxObjectTransferTimeout || len(o.contentType) > 255 || strings.ContainsAny(o.contentType, "\r\n\x00") {
		return o, errors.New("invalid transfer options")
	}
	return o, nil
}

func cmdBucketTransfer(args []string) int {
	o, err := parseBucketTransfer(args)
	if err != nil {
		PrintUsage(osStderr, bucketTransferUsage, "bucket")
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	result, err := runBucketTransfer(ctx, c, o)
	if result.Status != "" {
		if jsonOutput {
			if code := jsonOut(writeJSON(result)); code != 0 {
				return code
			}
		} else {
			_, _ = fmt.Fprintf(osStdout, "%s: %q (%d bytes)\n", result.Status, result.Key, result.Bytes)
			if result.UploadID != "" {
				_, _ = fmt.Fprintf(osStdout, "Upload: %s\n", result.UploadID)
			}
		}
	}
	if err != nil {
		return printErr("Object transfer failed", err)
	}
	return 0
}

func runBucketTransfer(ctx context.Context, c bucketTransferClient, o bucketTransferOptions) (bucketTransferResult, error) {
	if o.action == "download" {
		return downloadBucketFile(ctx, c, o)
	}
	initial, err := os.Lstat(o.path)
	if err != nil || !initial.Mode().IsRegular() {
		return bucketTransferResult{}, errors.New("upload source must be a regular file")
	}
	file, err := openCustomerFile(o.path)
	if err != nil {
		return bucketTransferResult{}, fmt.Errorf("open upload file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return bucketTransferResult{}, errors.New("upload source must be a regular file")
	}
	catalog, err := c.ListObjectBuckets(ctx, o.app)
	if err != nil {
		return bucketTransferResult{}, err
	}
	if !catalog.Enabled || catalog.MaxSinglePutBytes <= 0 || catalog.MaxUploadBytes < catalog.MaxSinglePutBytes || info.Size() > catalog.MaxUploadBytes {
		return bucketTransferResult{}, errors.New("upload exceeds the enabled service's size limit")
	}
	if info.Size() > catalog.MaxSinglePutBytes && catalog.MaxSinglePutBytes > 0 {
		return uploadBucketMultipart(ctx, c, o, file, info.Size())
	}
	size := info.Size()
	signed, err := c.SignBucketObject(ctx, o.app, o.bucket, api.ObjectSignRequest{Method: http.MethodPut, Key: o.key, SizeBytes: &size, ContentType: o.contentType})
	if err != nil {
		return bucketTransferResult{}, err
	}
	result := bucketTransferResult{Key: o.key, Path: o.path, Bytes: size, UploadID: signed.UploadID, Status: "pending"}
	response, err := executeBucketCapability(ctx, signed, http.MethodPut, file, size)
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	etag, err := bucketTransferETag(response)
	if err != nil {
		return result, err
	}
	result.ETag, result.Status = etag, "completed"
	return result, nil
}

func uploadBucketMultipart(ctx context.Context, c bucketTransferClient, o bucketTransferOptions, file *os.File, size int64) (bucketTransferResult, error) {
	u, err := c.CreateObjectMultipartUpload(ctx, o.app, o.bucket, api.CreateObjectMultipartUploadRequest{Key: o.key, SizeBytes: size, ContentType: o.contentType})
	if err != nil {
		return bucketTransferResult{}, err
	}
	result := bucketTransferResult{Key: o.key, Path: o.path, Bytes: size, UploadID: u.ID, Status: "pending"}
	id, idErr := uuid.Parse(u.ID)
	if idErr != nil || id == uuid.Nil || id.String() != u.ID || u.Key != o.key || u.SizeBytes != size || size <= 0 || u.PartSizeBytes <= 0 || u.PartSizeBytes > api.MaxObjectSinglePutBytes || u.PartCount < 1 || u.PartCount > api.MaxMultipartParts || int64(u.PartCount) != (size-1)/u.PartSizeBytes+1 {
		return result, errors.New("invalid multipart session geometry")
	}
	parts := make([]api.ObjectMultipartCompletedPart, 0, u.PartCount)
	for part := int32(1); part <= u.PartCount; part++ {
		offset := int64(part-1) * u.PartSizeBytes
		signed, err := c.SignObjectMultipartPart(ctx, o.app, o.bucket, u.ID, int(part), api.ObjectMultipartPartSignRequest{})
		if err != nil {
			return result, err
		}
		response, err := executeBucketCapability(ctx, signed, http.MethodPut, io.NewSectionReader(file, offset, min(u.PartSizeBytes, size-offset)), min(u.PartSizeBytes, size-offset))
		if err != nil {
			return result, err
		}
		etag, etagErr := bucketTransferETag(response)
		_ = response.Body.Close()
		if etagErr != nil {
			return result, etagErr
		}
		parts = append(parts, api.ObjectMultipartCompletedPart{PartNumber: part, ETag: etag})
	}
	done, err := c.CompleteObjectMultipartUpload(ctx, o.app, o.bucket, u.ID, api.CompleteObjectMultipartUploadRequest{Parts: parts})
	if err != nil {
		return result, err
	}
	if done.State != "completed" || done.ID != u.ID || done.Key != o.key || done.SizeBytes != size {
		return result, errors.New("multipart completion is pending; inspect the upload session")
	}
	result.ETag, result.Status = done.ETag, "completed"
	return result, nil
}

// Capability transfers use a fresh unauthenticated client. Neither redirects
// nor automatic retry can replay an admitted PUT or leak its signed URL.
func executeBucketCapability(ctx context.Context, signed api.ObjectSignedRequest, method string, body io.Reader, size int64) (*http.Response, error) {
	u, err := url.Parse(signed.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || signed.Method != method || signed.ExpiresAt.IsZero() || !signed.ExpiresAt.After(time.Now()) {
		return nil, errors.New("invalid or expired transfer capability")
	}
	var stream io.Reader
	if body != nil && size > 0 {
		stream = io.LimitReader(body, size)
	}
	r, err := http.NewRequestWithContext(ctx, method, signed.URL, stream)
	if err != nil {
		return nil, errors.New("invalid transfer request")
	}
	r.ContentLength = size
	r.Header.Set("Accept-Encoding", "gzip")
	for name, value := range signed.Headers {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Host") {
			return nil, errors.New("unexpected authentication header in transfer capability")
		}
		if strings.EqualFold(name, "Content-Length") {
			if value != strconv.FormatInt(size, 10) {
				return nil, errors.New("transfer size differs from capability")
			}
			continue
		}
		r.Header.Set(name, value)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return nil, errors.New("transfer interrupted; its outcome may be pending")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || method == http.MethodGet && response.StatusCode != http.StatusOK || response.Uncompressed {
		_ = response.Body.Close()
		return nil, fmt.Errorf("transfer rejected (HTTP %d)", response.StatusCode)
	}
	return response, nil
}

func bucketTransferETag(response *http.Response) (string, error) {
	values := response.Header.Values("ETag")
	if len(values) != 1 || values[0] == "" || len(values[0]) > api.MaxObjectWriteETagBytes || !utf8.ValidString(values[0]) || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", errors.New("transfer acknowledgment has no valid ETag; inspect the upload session")
	}
	return values[0], nil
}

func downloadBucketFile(ctx context.Context, c bucketTransferClient, o bucketTransferOptions) (bucketTransferResult, error) {
	if _, err := os.Lstat(o.path); err == nil && !o.force {
		return bucketTransferResult{}, errors.New("destination exists; use --force to replace it")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return bucketTransferResult{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(o.path), ".gregale-download-*")
	if err != nil {
		return bucketTransferResult{}, fmt.Errorf("create download file: %w", err)
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	signed, err := c.SignBucketObject(ctx, o.app, o.bucket, api.ObjectSignRequest{Method: http.MethodGet, Key: o.key})
	if err != nil {
		return bucketTransferResult{}, err
	}
	response, err := executeBucketCapability(ctx, signed, http.MethodGet, nil, 0)
	if err != nil {
		return bucketTransferResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	n, err := io.Copy(file, response.Body)
	if err != nil || response.ContentLength >= 0 && n != response.ContentLength {
		return bucketTransferResult{}, errors.New("download interrupted; destination preserved")
	}
	if err = file.Sync(); err != nil {
		return bucketTransferResult{}, err
	}
	if err = file.Close(); err != nil {
		return bucketTransferResult{}, err
	}
	if o.force {
		err = os.Rename(file.Name(), o.path)
	} else {
		err = os.Link(file.Name(), o.path)
	}
	if err != nil {
		return bucketTransferResult{}, fmt.Errorf("publish download file: %w", err)
	}
	return bucketTransferResult{Key: o.key, Path: o.path, Bytes: n, ETag: response.Header.Get("ETag"), Status: "completed"}, nil
}

func cmdObjectStorageUsage(args []string) int {
	if len(args) != 0 {
		PrintUsage(osStderr, "usage: gregale usage object-storage", "usage")
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	u, err := c.GetObjectStorageUsage(context.Background())
	if err != nil {
		return printErr("Could not read object storage usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(u))
	}
	_, _ = fmt.Fprintf(osStdout, "Object storage observed: %d bytes; reserved keys: %d\nRequests: %d; egress: %d bytes; authorizations: %d\nFresh: %t; billing: %s\n", u.Usage.ObservedBytes, u.Usage.CapacityKeys, u.Usage.RequestCount, u.Usage.EgressBytes, u.Usage.Authorizations, u.Usage.Fresh, u.BillingMode)
	if len(u.Usage.UnavailableMeters) != 0 {
		_, _ = fmt.Fprintf(osStdout, "Unavailable meters (numeric values are unknown): %s\n", strings.Join(u.Usage.UnavailableMeters, ", "))
	}
	if u.Charges != nil {
		_, _ = fmt.Fprintf(osStdout, "Estimated charge: %d millicents %s\n", u.Charges.TotalMillicents, u.Charges.Currency)
	}
	return 0
}
