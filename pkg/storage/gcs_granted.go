package storage

// adr: 20261117190000 — compute reaches the shared store only through
// per-object signed grants.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"

	"github.com/onebox-faas/faas/pkg/storagegrant"
)

// ObjectGranter asks the control plane for signed object grants, in request
// order (secretd's GrantStorageObjects).
type ObjectGranter interface {
	GrantObjects(ctx context.Context, requests []storagegrant.Request) ([]storagegrant.Grant, error)
}

// grantMargin is how long before its expiry a cached read grant is renewed:
// a large download that starts on a grant must not outlive it mid-request
// in a way the store notices (GCS checks expiry only when a request starts).
const grantMargin = 30 * time.Second

// maxCachedGrants bounds the read-grant cache.
const maxCachedGrants = 4096

// grantedGCSStore implements the GCS store interfaces over signed URLs: it
// holds no store credential. Deletes and generation retirements never reach
// it (the backend defers them to the control plane, ADR-20261115180000), and
// listing is the control plane's job.
type grantedGCSStore struct {
	granter ObjectGranter
	client  *http.Client
	now     func() time.Time

	mu    sync.Mutex
	reads map[grantCacheKey]storagegrant.Grant
}

type grantCacheKey struct {
	key        string
	op         storagegrant.Op
	generation int64
}

var (
	_ gcsArtifactStore          = (*grantedGCSStore)(nil)
	_ gcsExclusiveArtifactStore = (*grantedGCSStore)(nil)
	_ gcsExclusiveReceiptStore  = (*grantedGCSStore)(nil)
)

// ErrGrantedStoreCannotDelete is returned when a delete reaches a store that
// holds only grants; deletes must be deferred to the control plane.
var ErrGrantedStoreCannotDelete = errors.New("storage: a granted store cannot delete; deletes go through the control plane")

func newGrantedGCSStore(granter ObjectGranter, client *http.Client) *grantedGCSStore {
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	return &grantedGCSStore{granter: granter, client: client, now: time.Now, reads: map[grantCacheKey]storagegrant.Grant{}}
}

func (s *grantedGCSStore) grant(ctx context.Context, r storagegrant.Request) (storagegrant.Grant, error) {
	cacheable := !r.Op.Writes()
	ck := grantCacheKey{key: r.Key, op: r.Op, generation: r.Generation}
	if cacheable {
		s.mu.Lock()
		g, ok := s.reads[ck]
		s.mu.Unlock()
		if ok && s.now().Add(grantMargin).Before(g.Expires) {
			return g, nil
		}
	}
	grants, err := s.granter.GrantObjects(ctx, []storagegrant.Request{r})
	if err != nil {
		return storagegrant.Grant{}, fmt.Errorf("storage: grant %s %q: %w", r.Op, r.Key, err)
	}
	if len(grants) != 1 || grants[0].URL == "" {
		return storagegrant.Grant{}, fmt.Errorf("storage: grant %s %q: the control plane answered %d grants", r.Op, r.Key, len(grants))
	}
	g := grants[0]
	if cacheable {
		s.mu.Lock()
		if len(s.reads) >= maxCachedGrants {
			now := s.now()
			for k, v := range s.reads {
				if !now.Add(grantMargin).Before(v.Expires) {
					delete(s.reads, k)
				}
			}
			if len(s.reads) >= maxCachedGrants {
				clear(s.reads)
			}
		}
		s.reads[ck] = g
		s.mu.Unlock()
	}
	return g, nil
}

// forget drops a cached read grant the store refused (expired early, or
// revoked by a credential rotation on the control plane).
func (s *grantedGCSStore) forget(r storagegrant.Request) {
	s.mu.Lock()
	delete(s.reads, grantCacheKey{key: r.Key, op: r.Op, generation: r.Generation})
	s.mu.Unlock()
}

// send performs g with body (nil for reads) and extra unsigned headers.
func (s *grantedGCSStore) send(ctx context.Context, g storagegrant.Grant, body io.Reader, extra map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, g.Method, g.URL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range g.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	return s.client.Do(req)
}

// read performs a read grant, renewing it once if the store says it expired.
func (s *grantedGCSStore) read(ctx context.Context, r storagegrant.Request, extra map[string]string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		g, err := s.grant(ctx, r)
		if err != nil {
			return nil, err
		}
		resp, err := s.send(ctx, g, nil, extra)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			s.forget(r)
			if attempt == 0 {
				drain(resp)
				continue
			}
		}
		return resp, nil
	}
}

// statusError maps a store response to the errors the GCS backend already
// understands: missing objects and failed preconditions.
func statusError(resp *http.Response) error {
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return gcs.ErrObjectNotExist
	case http.StatusPreconditionFailed:
		return &googleapi.Error{Code: http.StatusPreconditionFailed, Message: "precondition failed"}
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return &googleapi.Error{Code: resp.StatusCode, Message: strings.TrimSpace(string(snippet))}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// metadataFrom collects x-goog-meta-* response headers.
func metadataFrom(h http.Header) map[string]string {
	out := map[string]string{}
	for name, values := range h {
		if meta, ok := strings.CutPrefix(strings.ToLower(name), "x-goog-meta-"); ok && len(values) > 0 {
			out[meta] = values[0]
		}
	}
	return out
}

func generationFrom(h http.Header) (int64, error) {
	g, err := strconv.ParseInt(h.Get("x-goog-generation"), 10, 64)
	if err != nil || g <= 0 {
		return 0, fmt.Errorf("storage: the store answered without a generation")
	}
	return g, nil
}

// Get implements gcsArtifactStore. One GET returns the body, its
// generation and metadata together, so they always match.
func (s *grantedGCSStore) Get(ctx context.Context, _, key string) (gcsArtifact, error) {
	resp, err := s.read(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpGet}, nil)
	if err != nil {
		return gcsArtifact{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return gcsArtifact{}, statusError(resp)
	}
	generation, err := generationFrom(resp.Header)
	if err != nil {
		drain(resp)
		return gcsArtifact{}, err
	}
	return gcsArtifact{body: resp.Body, metadata: metadataFrom(resp.Header), generation: generation}, nil
}

// currentGeneration probes key's generation with a one-byte read; 0 means
// the key holds nothing.
func (s *grantedGCSStore) currentGeneration(ctx context.Context, key string) (int64, error) {
	resp, err := s.read(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpGet}, map[string]string{"Range": "bytes=0-0"})
	if err != nil {
		return 0, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
		defer drain(resp)
		return generationFrom(resp.Header)
	case http.StatusNotFound:
		drain(resp)
		return 0, nil
	}
	return 0, statusError(resp)
}

// write uploads body under a create or replace grant and returns the new
// generation and the bytes sent.
func (s *grantedGCSStore) write(ctx context.Context, r storagegrant.Request, body io.Reader) (int64, int64, error) {
	r.ContentType = gcsContentType
	g, err := s.grant(ctx, r)
	if err != nil {
		return 0, 0, err
	}
	counted := &countingReader{r: body}
	resp, err := s.send(ctx, g, counted, nil)
	if err != nil {
		return 0, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, 0, statusError(resp)
	}
	defer drain(resp)
	generation, err := generationFrom(resp.Header)
	return generation, counted.n, err
}

// Put implements gcsArtifactStore: create when the key holds nothing,
// otherwise replace exactly the generation it holds. A writer racing in
// between fails the precondition rather than being overwritten blindly.
func (s *grantedGCSStore) Put(ctx context.Context, _, key string, body io.Reader, metadata map[string]string) error {
	current, err := s.currentGeneration(ctx, key)
	if err != nil {
		return err
	}
	r := storagegrant.Request{Key: key, Op: storagegrant.OpCreate, Metadata: metadata}
	if current > 0 {
		r.Op, r.Generation = storagegrant.OpReplace, current
	}
	_, _, err = s.write(ctx, r, body)
	return err
}

// PutExclusive implements gcsExclusiveArtifactStore.
func (s *grantedGCSStore) PutExclusive(ctx context.Context, _, key string, body io.Reader, metadata map[string]string) error {
	_, _, err := s.write(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpCreate, Metadata: metadata}, body)
	return err
}

// PutExclusiveReceipt implements gcsExclusiveReceiptStore: the receipt is
// the upload response's generation and the bytes this call sent.
func (s *grantedGCSStore) PutExclusiveReceipt(ctx context.Context, _, key string, body io.Reader, metadata map[string]string) (gcsExclusiveObjectReceipt, error) {
	generation, size, err := s.write(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpCreate, Metadata: metadata}, body)
	if err != nil {
		return gcsExclusiveObjectReceipt{}, err
	}
	if generation <= 0 || size <= 0 {
		return gcsExclusiveObjectReceipt{}, ErrArtifactReceiptMismatch
	}
	return gcsExclusiveObjectReceipt{Generation: generation, Size: size}, ctx.Err()
}

// GetGeneration implements gcsExclusiveReceiptStore.
func (s *grantedGCSStore) GetGeneration(ctx context.Context, _, key string, generation, size int64) (io.ReadCloser, error) {
	resp, err := s.read(ctx, storagegrant.Request{Key: key, Op: storagegrant.OpGetGeneration, Generation: generation}, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	got, gerr := generationFrom(resp.Header)
	if gerr != nil || got != generation || resp.ContentLength != size || resp.Header.Get("Content-Encoding") != "" {
		drain(resp)
		return nil, ErrArtifactReceiptMismatch
	}
	return resp.Body, nil
}

// RetireGeneration implements gcsExclusiveReceiptStore; it is never reached
// in defer mode (RetireExclusiveArtifact records the retirement instead).
func (s *grantedGCSStore) RetireGeneration(context.Context, string, string, int64) error {
	return ErrGrantedStoreCannotDelete
}

// Delete implements gcsArtifactStore; deferred deletes never reach it.
func (s *grantedGCSStore) Delete(context.Context, string, string) error {
	return ErrGrantedStoreCannotDelete
}

// List implements gcsArtifactStore. A node holding grants cannot list the
// bucket, and the sweeps that list (imaged job artifacts, builderd sources)
// are the control plane's in this mode: report nothing to sweep.
func (s *grantedGCSStore) List(context.Context, string, string) ([]string, error) {
	return nil, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// UseGrants makes every GCS and S3 backend under b reach its bucket only through
// granter (ADR-20261117190000), walking composites as DeferDeletes does. It
// returns how many backends it switched. Each one must already defer its
// deletes: a node with grants has no delete right.
func UseGrants(b StorageBackend, granter ObjectGranter, client *http.Client) (int, error) {
	if granter == nil {
		return 0, errors.New("storage: UseGrants needs a granter")
	}
	switch be := b.(type) {
	case *GCSStorageBackend:
		if be.deferDelete == nil {
			return 0, errors.New("storage: a granted GCS backend must defer its deletes first (FAAS_STORAGE_DELETE_MODE=defer)")
		}
		be.store = newGrantedGCSStore(granter, client)
		return 1, nil
	case *S3StorageBackend:
		if be.deferDelete == nil {
			return 0, errors.New("storage: a granted S3 backend must defer its deletes first (FAAS_STORAGE_DELETE_MODE=defer)")
		}
		be.store = newGrantedS3Store(granter, client)
		return 1, nil
	case *LocalCacheBackend:
		return UseGrants(be.parent, granter, client)
	case *PrefixRouter:
		n := 0
		for prefix, route := range be.routes {
			c, err := UseGrants(route, granter, client)
			if err != nil {
				return 0, fmt.Errorf("route %q: %w", prefix, err)
			}
			n += c
		}
		if be.fallback != nil {
			c, err := UseGrants(be.fallback, granter, client)
			if err != nil {
				return 0, err
			}
			n += c
		}
		return n, nil
	case *FallbackStorageBackend:
		a, err := UseGrants(be.primary, granter, client)
		if err != nil {
			return 0, err
		}
		c, err := UseGrants(be.fallback, granter, client)
		if err != nil {
			return 0, err
		}
		return a + c, nil
	case *LocalStorageBackend:
		return 0, nil
	default:
		return 0, fmt.Errorf("storage: %T cannot reach its store through grants", b)
	}
}
