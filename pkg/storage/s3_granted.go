package storage

// adr: 20261117190000 — slice 4: the S3 artifact store through grants.

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3's Content-MD5 integrity header, not a security hash
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/storagegrant"
)

// grantedS3Store implements s3ArtifactStore over signed URLs: it holds no S3
// key. Writes are create-only (If-None-Match: *) or replace exactly the ETag
// the key held, single PUTs below one part and multipart uploads above, with
// every request a grant. Deletes are deferred and never reach it; listing
// and server-side copies are the control plane's.
type grantedS3Store struct {
	grants *grantedGCSStore // the shared grant cache and HTTP sender
}

var _ s3ArtifactStore = (*grantedS3Store)(nil)

func newGrantedS3Store(granter ObjectGranter, client *http.Client) *grantedS3Store {
	return &grantedS3Store{grants: newGrantedGCSStore(granter, client)}
}

// s3StatusError maps a store response to the errors the S3 backend knows.
func s3StatusError(resp *http.Response) error {
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusPreconditionFailed, http.StatusConflict:
		return fmt.Errorf("%w: the key changed since it was read (HTTP %d)", ErrArtifactExists, resp.StatusCode)
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("storage: s3 grant request answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

func s3MetadataFrom(h http.Header) map[string]string {
	out := map[string]string{}
	for name, values := range h {
		if meta, ok := strings.CutPrefix(strings.ToLower(name), "x-amz-meta-"); ok && len(values) > 0 {
			out[meta] = values[0]
		}
	}
	return out
}

func (s *grantedS3Store) Get(ctx context.Context, _, key string) (s3Artifact, error) {
	resp, err := s.grants.read(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpGet}, nil) //nolint:bodyclose // the body is the artifact the caller closes
	if err != nil {
		return s3Artifact{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return s3Artifact{}, s3StatusError(resp)
	}
	return s3Artifact{body: resp.Body, metadata: s3MetadataFrom(resp.Header)}, nil
}

// head returns the key's size and ETag; ErrNotFound when it holds nothing.
func (s *grantedS3Store) head(ctx context.Context, key string) (int64, string, error) {
	resp, err := s.grants.read(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpHead}, nil)
	if err != nil {
		return 0, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, "", s3StatusError(resp)
	}
	defer drain(resp)
	return resp.ContentLength, resp.Header.Get("ETag"), nil
}

func (s *grantedS3Store) Head(ctx context.Context, _, key string) (int64, error) {
	size, _, err := s.head(ctx, key)
	return size, err
}

// do performs a write grant with body.
func (s *grantedS3Store) do(ctx context.Context, r storagegrant.Request, body []byte) (*http.Response, error) {
	g, err := s.grants.grant(ctx, r)
	if err != nil {
		return nil, err
	}
	extra := map[string]string{}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
		if r.Op == storagegrant.OpCreate || r.Op == storagegrant.OpReplace || r.Op == storagegrant.OpUploadPart {
			sum := md5.Sum(body) //nolint:gosec // see import
			extra["Content-MD5"] = base64.StdEncoding.EncodeToString(sum[:])
		}
	}
	return s.grants.send(ctx, g, reader, extra)
}

// Put writes body under key: create-only when the key holds nothing,
// otherwise replace exactly the ETag it holds.
func (s *grantedS3Store) Put(ctx context.Context, _, key string, body io.Reader, metadata map[string]string) error {
	_, etag, err := s.head(ctx, key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	buf := make([]byte, s3PartSize)
	n, err := readPart(ctx, body, buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if errors.Is(err, io.EOF) {
		r := storagegrant.Request{Key: key, Op: storagegrant.OpCreate, ContentType: gcsContentType, Metadata: metadata}
		if etag != "" {
			r.Op, r.ETag = storagegrant.OpReplace, etag
		}
		resp, err := s.do(ctx, r, buf[:n])
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return s3StatusError(resp)
		}
		drain(resp)
		return nil
	}
	return s.putMultipart(ctx, key, etag, metadata, body, buf, n)
}

type initiateMultipartResult struct {
	UploadID string `xml:"UploadId"`
}

type completeMultipartUpload struct {
	XMLName xml.Name       `xml:"CompleteMultipartUpload"`
	Parts   []completePart `xml:"Part"`
}

type completePart struct {
	PartNumber int32  `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func (s *grantedS3Store) putMultipart(ctx context.Context, key, etag string, metadata map[string]string, body io.Reader, buf []byte, n int) error {
	resp, err := s.do(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpCreateMultipart, ContentType: gcsContentType, Metadata: metadata}, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return s3StatusError(resp)
	}
	var created initiateMultipartResult
	err = xml.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&created)
	drain(resp)
	if err != nil {
		return fmt.Errorf("storage: s3 multipart upload of %q: decode upload id: %w", key, err)
	}
	if created.UploadID == "" {
		return fmt.Errorf("storage: s3 multipart upload of %q: no upload id", key)
	}
	upload := created.UploadID
	abort := func(cause error) error {
		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		resp, err := s.do(abortCtx, storagegrant.Request{Key: key, Op: storagegrant.OpAbortMultipart, UploadID: upload}, nil)
		if err == nil {
			if resp.StatusCode/100 != 2 {
				err = s3StatusError(resp)
			} else {
				drain(resp)
			}
		}
		if err != nil {
			return errors.Join(cause, fmt.Errorf("abort multipart upload: %w", err))
		}
		return cause
	}
	var parts []completePart
	for part := int32(1); ; part++ {
		if part > s3MaxParts {
			return abort(fmt.Errorf("artifact exceeds %d parts of %d bytes", s3MaxParts, s3PartSize))
		}
		resp, err := s.do(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpUploadPart, UploadID: upload, PartNumber: part}, buf[:n])
		if err != nil {
			return abort(err)
		}
		if resp.StatusCode != http.StatusOK {
			return abort(s3StatusError(resp))
		}
		parts = append(parts, completePart{PartNumber: part, ETag: resp.Header.Get("ETag")})
		drain(resp)
		var rerr error
		n, rerr = readPart(ctx, body, buf)
		if errors.Is(rerr, io.EOF) && n == 0 {
			break
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return abort(rerr)
		}
	}
	manifest, err := xml.Marshal(completeMultipartUpload{Parts: parts})
	if err != nil {
		return abort(err)
	}
	resp, err = s.do(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpCompleteMultipart, UploadID: upload, ETag: etag}, manifest)
	if err != nil {
		return abort(err)
	}
	if resp.StatusCode != http.StatusOK {
		return abort(s3StatusError(resp))
	}
	// S3 can answer 200 with an error document once the parts are joined.
	tail, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	drain(resp)
	if bytes.Contains(tail, []byte("<Error>")) {
		return abort(fmt.Errorf("storage: s3 complete multipart upload of %q: %s", key, strings.TrimSpace(string(tail))))
	}
	return nil
}

// Delete implements s3ArtifactStore; deferred deletes never reach it.
func (s *grantedS3Store) Delete(context.Context, string, string) error {
	return ErrGrantedStoreCannotDelete
}

// List reports nothing to sweep: a granted node cannot list the bucket.
func (s *grantedS3Store) List(context.Context, string, string) ([]string, error) {
	return nil, nil
}

// Copy is unsupported: the caller streams the object instead.
func (s *grantedS3Store) Copy(context.Context, string, string, string) error {
	return errors.ErrUnsupported
}
