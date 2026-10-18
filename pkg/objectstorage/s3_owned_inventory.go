package objectstorage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

// ListOwnedInventory is an operator-only extension. Managed providers never
// receive it; an opted-in node without the extension fails closed.
func (p *S3) ListOwnedInventory(ctx context.Context, bucket, kind, cursor string, limit int32) (objectinventory.Page, error) {
	if !p.ownedNodeReceipts {
		return objectinventory.Page{}, ErrConfiguration
	}
	if limit < 1 || limit > api.MaxObjectS3ListItems || len(cursor) > api.MaxObjectS3ListCursorBytes || kind != objectinventory.Buckets && kind != objectinventory.Objects && kind != objectinventory.Uploads || kind == objectinventory.Buckets && bucket != "" || kind != objectinventory.Buckets && !objectinventory.ValidBucket(bucket) {
		return objectinventory.Page{}, ErrInvalid
	}
	query := url.Values{objectinventory.Query: {kind}, "max-keys": {strconv.Itoa(int(limit))}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	body, err := p.ownedInventoryGET(ctx, bucket, query)
	if err != nil {
		return objectinventory.Page{}, err
	}
	var page objectinventory.Page
	if json.Unmarshal(body, &page) != nil || !validOwnedInventoryPage(page, bucket, kind, cursor, limit) {
		return objectinventory.Page{}, ErrUnavailable
	}
	return page, nil
}

func (p *S3) ownedInventoryGET(ctx context.Context, bucket string, query url.Values) ([]byte, error) {
	options := p.client.Options()
	if !p.ownedNodeReceipts || options.BaseEndpoint == nil {
		return nil, ErrConfiguration
	}
	u, err := url.Parse(*options.BaseEndpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrConfiguration
	}
	u.Path, u.RawPath, u.RawQuery = strings.TrimRight(u.Path, "/")+"/"+bucket, "", query.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrInvalid
	}
	credential, err := options.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, ErrConfiguration
	}
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	r.Header.Set("X-Amz-Content-Sha256", emptySHA256)
	if err = v4.NewSigner().SignHTTP(ctx, credential, r, emptySHA256, "s3", p.region, time.Now().UTC()); err != nil {
		return nil, ErrConfiguration
	}
	response, err := options.HTTPClient.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ErrConfiguration
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.ObjectNodeInventoryMaxResponseBytes+1))
	if err != nil || len(body) > api.ObjectNodeInventoryMaxResponseBytes {
		return nil, ErrUnavailable
	}
	return body, nil
}

func validInventoryUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func validInventorySnapshot(snapshot string) bool {
	boot, epoch, ok := strings.Cut(snapshot, ":")
	_, err := strconv.ParseUint(epoch, 10, 64)
	return ok && validInventoryUUID(boot) && err == nil
}

func validOwnedInventoryPage(p objectinventory.Page, bucket, kind, cursor string, limit int32) bool {
	if p.Version != objectinventory.Version || p.Kind != kind || p.Bucket != bucket || !validInventorySnapshot(p.Snapshot) || p.ObservedAt.IsZero() || p.ObservedAt.After(time.Now().UTC()) || time.Since(p.ObservedAt) > time.Duration(api.ObjectStorageInventoryMaxAgeSeconds)*time.Second || len(p.NextCursor) > api.MaxObjectS3ListCursorBytes || p.NextCursor != "" && p.NextCursor == cursor {
		return false
	}
	if kind == objectinventory.Buckets {
		if p.Generation != "" || len(p.Objects)+len(p.Uploads) != 0 {
			return false
		}
		last := ""
		for _, b := range p.Buckets {
			if !objectinventory.ValidBucket(b.Name) || !validInventoryUUID(b.Generation) || b.Name <= last || b.CreatedAt.IsZero() {
				return false
			}
			last = b.Name
		}
	} else if !validInventoryUUID(p.Generation) || len(p.Buckets) != 0 {
		return false
	}
	if kind == objectinventory.Objects {
		if len(p.Uploads) != 0 {
			return false
		}
		last := ""
		for _, o := range p.Objects {
			sha, err := hex.DecodeString(o.SHA256)
			if !ValidKey(o.Key) || o.Key <= last || !validInventoryUUID(o.ID) || o.Size < 0 || o.Size > api.MaxObjectUploadBytes || !validUploadETag(o.ETag) || o.Modified.IsZero() || err != nil || len(sha) != 32 {
				return false
			}
			last = o.Key
		}
	}
	if kind == objectinventory.Uploads {
		if len(p.Objects) != 0 {
			return false
		}
		last := ""
		for _, u := range p.Uploads {
			id := u.Key + "\x00" + u.ID
			if !ValidKey(u.Key) || !validInventoryUUID(u.ID) || id <= last || u.Status != "active" && u.Status != "completing" || u.Size < 0 || u.Size > api.MaxObjectUploadBytes || u.Parts < 0 || u.Parts > api.MaxMultipartParts || u.CreatedAt.IsZero() || !u.ExpiresAt.After(u.CreatedAt) {
				return false
			}
			last = id
		}
	}
	count := len(p.Buckets) + len(p.Objects) + len(p.Uploads)
	return count <= int(limit) && (p.NextCursor == "" || count != 0)
}

// CompleteOwnedInventory feeds the existing capacity/accounting boundary. A
// revision change, corrupt blob, missing field or partial scan changes no total.
// The bool distinguishes managed providers, which keep their existing protocol.
func CompleteOwnedInventory(ctx context.Context, provider Provider, bucket string, record func(context.Context) error) (bytes, keys int64, owned bool, err error) {
	p, ok := provider.(*S3)
	if !ok || !p.ownedNodeReceipts {
		return 0, 0, false, nil
	}
	cursor, snapshot, generation, last := "", "", "", ""
	var observed time.Time
	seen := map[string]bool{}
	for range api.ObjectStorageInventoryMaxPages {
		if record != nil {
			if err = record(ctx); err != nil {
				return 0, 0, true, err
			}
		}
		page, e := p.ListOwnedInventory(ctx, bucket, objectinventory.Objects, cursor, api.MaxObjectS3ListItems)
		if e != nil {
			return 0, 0, true, e
		}
		if snapshot == "" {
			snapshot, generation, observed = page.Snapshot, page.Generation, page.ObservedAt
		}
		if page.Snapshot != snapshot || page.Generation != generation || !page.ObservedAt.Equal(observed) {
			return 0, 0, true, ErrUnavailable
		}
		if cursor != "" && len(page.Objects) == 0 {
			return 0, 0, true, ErrUnavailable
		}
		for _, o := range page.Objects {
			if o.Key <= last || o.Size > api.MaxObjectStoragePolicyValue-bytes {
				return 0, 0, true, ErrUnavailable
			}
			last = o.Key
			bytes += o.Size
			keys++
		}
		if page.NextCursor == "" {
			return bytes, keys, true, nil
		}
		if seen[page.NextCursor] {
			return 0, 0, true, ErrUnavailable
		}
		seen[page.NextCursor], cursor = true, page.NextCursor
	}
	return 0, 0, true, ErrUnavailable
}
