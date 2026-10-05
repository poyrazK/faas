package s3gateway

import (
	"encoding/xml"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type versionTaggingHTTPFixture struct {
	mu       sync.Mutex
	tags     map[string]map[string]string
	current  string
	lose     bool
	calls    int
	receipts map[string]string
}

func newVersionTaggingHTTPFixture() *versionTaggingHTTPFixture {
	return &versionTaggingHTTPFixture{current: publicVersionNativeNew, tags: map[string]map[string]string{publicVersionNativeOld: {"generation": "old"}, publicVersionNativeNew: {"generation": "new"}, "null": {"generation": "null"}}, receipts: map[string]string{publicVersionNativeOld: "retained-write-proof", publicVersionNativeNew: "current-write-proof"}}
}
func (p *versionTaggingHTTPFixture) count() int { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }
func (p *versionTaggingHTTPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	q := r.URL.Query()
	if !q.Has("tagging") || r.Header.Get("Authorization") == "" || strings.TrimPrefix(r.URL.Path, "/physical/") != publicVersionTestKey {
		t.Error("tagging left owned object", r.URL)
		w.WriteHeader(500)
		return
	}
	version := q.Get("versionId")
	if version == "" {
		version = p.current
	}
	if version == "private-marker" {
		w.WriteHeader(405)
		_, _ = io.WriteString(w, `<Error><Code>MethodNotAllowed</Code><Message>private marker</Message></Error>`)
		return
	}
	tags, exists := p.tags[version]
	if !exists {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchVersion</Code><Message>private version</Message></Error>`)
		return
	}
	w.Header().Set("X-Amz-Version-Id", version)
	switch r.Method {
	case http.MethodGet:
		_ = xml.NewEncoder(w).Encode(objectTaggingResult{XMLNS: s3XMLNamespace, Tags: objectTagSet(tags)})
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		tags, parse := objectstorage.ParseObjectTaggingXML(body)
		if err != nil || parse != nil {
			t.Error(err, parse)
			w.WriteHeader(400)
			return
		}
		p.tags[version] = maps.Clone(tags)
		if p.lose {
			p.lose = false
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}
	case http.MethodDelete:
		p.tags[version] = map[string]string{}
		w.WriteHeader(http.StatusNoContent)
	default:
		t.Error(r.Method)
		w.WriteHeader(500)
	}
}

// adr: 549
func TestVersionTaggingEndToEndMem(t *testing.T) {
	versionTaggingEndToEnd(t, state.NewMemStore(), nil)
}
func TestVersionTaggingEndToEndPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	versionTaggingEndToEnd(t, st, func() multipartCopyIntegrationStore { return state.NewPgStore(pool) })
}
func versionTaggingEndToEnd(t *testing.T, st multipartCopyIntegrationStore, restart func() multipartCopyIntegrationStore) {
	p := newVersionTaggingHTTPFixture()
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: publicVersionTestKey, ProviderVersionID: publicVersionNativeOld}, {Key: publicVersionTestKey, ProviderVersionID: "private-marker", DeleteMarker: true}})
	if err != nil {
		t.Fatal(err)
	}
	oldID := refs[0].ID
	before, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey)})
	if err != nil || !state.ValidObjectVersionID(aws.ToString(current.VersionId)) || aws.ToString(current.VersionId) == oldID || len(current.TagSet) != 1 || aws.ToString(current.TagSet[0].Value) != "new" {
		t.Fatal(current, err)
	}
	p.mu.Lock()
	p.lose = true
	p.mu.Unlock()
	input := &awss3.PutObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(oldID), Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("目录"), Value: aws.String("new & value")}}}}
	if _, err = f.client.PutObjectTagging(t.Context(), input); err == nil || p.count() != 2 {
		t.Fatal("lost acknowledgment was replayed", err, p.count())
	}
	if restart != nil {
		st = restart()
		f.handler.store = st
	}
	out, err := f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(oldID)})
	if err != nil || aws.ToString(out.VersionId) != oldID || len(out.TagSet) != 1 || aws.ToString(out.TagSet[0].Value) != "new & value" {
		t.Fatal("restart changed selected identity", out, err)
	}
	if ack, err := f.client.PutObjectTagging(t.Context(), input); err != nil || aws.ToString(ack.VersionId) != oldID {
		t.Fatal("explicit retry", ack, err)
	}
	if ack, err := f.client.DeleteObjectTagging(t.Context(), &awss3.DeleteObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(oldID)}); err != nil || aws.ToString(ack.VersionId) != oldID {
		t.Fatal(ack, err)
	}
	if ack, err := f.client.PutObjectTagging(t.Context(), &awss3.PutObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String("null"), Tagging: &types.Tagging{TagSet: []types.Tag{}}}); err != nil || aws.ToString(ack.VersionId) != "null" {
		t.Fatal(ack, err)
	}
	if empty, err := f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String("null")}); err != nil || aws.ToString(empty.VersionId) != "null" || len(empty.TagSet) != 0 {
		t.Fatal("empty tag set", empty, err)
	}
	p.mu.Lock()
	if len(p.tags[publicVersionNativeOld]) != 0 || p.tags[publicVersionNativeNew]["generation"] != "new" || p.receipts[publicVersionNativeOld] != "retained-write-proof" || len(p.tags) != 3 {
		t.Error("tagging changed data history or proof", p.tags, p.receipts)
	}
	p.mu.Unlock()
	after, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || before.Buckets[0].BaselineBytes != after.Buckets[0].BaselineBytes || before.Buckets[0].GrantedBytes != after.Buckets[0].GrantedBytes || before.Buckets[0].GrantedKeys != after.Buckets[0].GrantedKeys {
		t.Fatal("tagging changed reserved capacity", before, after, err)
	}
	count := p.count()
	for _, tc := range []struct{ key, version string }{{"wrong-key", oldID}, {publicVersionTestKey, uuid.NewString()}, {publicVersionTestKey, publicVersionNativeOld}} {
		if _, err = f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(tc.key), VersionId: aws.String(tc.version)}); err == nil || p.count() != count {
			t.Fatal("unowned version reached provider", tc, err, p.count())
		}
	}
	_, err = f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(refs[1].ID)})
	var service smithy.APIError
	if !errors.As(err, &service) || service.ErrorCode() != "MethodNotAllowed" || strings.Contains(err.Error(), "private marker") {
		t.Fatal("marker error not sanitized", err)
	}
}

func TestVersionTaggingInputAndPermissions(t *testing.T) {
	p := newVersionTaggingHTTPFixture()
	f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }), state.ObjectBucketPermissionRead)
	if _, err := f.client.PutObjectTagging(t.Context(), &awss3.PutObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), Tagging: &types.Tagging{TagSet: []types.Tag{}}}); err == nil || p.count() != 0 {
		t.Fatal("read-only credential mutated tags", err)
	}
	for _, query := range []string{"tagging&versionId=", "tagging=unexpected", "tagging&versionId=null&versionId=null", "tagging&uploadId=wrong"} {
		request := signedGatewayRequest(t, http.MethodGet, "http://"+f.handler.host+"/assets/"+url.PathEscape(publicVersionTestKey)+"?"+query, nil, "UNSIGNED-PAYLOAD")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, request)
		if w.Code < 400 || p.count() != 0 {
			t.Fatal("ambiguous tag request reached provider", query, w.Code, p.count())
		}
	}
	r := signedGatewayRequest(t, http.MethodGet, "http://"+f.handler.host+"/assets/"+url.PathEscape(publicVersionTestKey)+"?tagging", nil, "UNSIGNED-PAYLOAD")
	r.URL.RawQuery += "&versionId=%ZZ"
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code < 400 || p.count() != 0 {
		t.Fatal("malformed selector became current tagging", w.Code, p.count())
	}
}

func TestVersionTaggingDisabledCleanup(t *testing.T) {
	p := newVersionTaggingHTTPFixture()
	f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	var enabled atomic.Bool
	f.handler.enabled = enabled.Load
	if _, err := f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String("null")}); err == nil || p.count() != 0 {
		t.Fatal("disabled ingress served tags", err, p.count())
	}
	if _, err := f.client.PutObjectTagging(t.Context(), &awss3.PutObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String("null"), Tagging: &types.Tagging{TagSet: []types.Tag{}}}); err == nil || p.count() != 0 {
		t.Fatal("disabled ingress replaced tags", err, p.count())
	}
	if out, err := f.client.DeleteObjectTagging(t.Context(), &awss3.DeleteObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String("null")}); err != nil || aws.ToString(out.VersionId) != "null" || p.count() != 1 {
		t.Fatal("disabled tag cleanup blocked", out, err, p.count())
	}
	enabled.Store(true)
	if out, err := f.client.GetObjectTagging(t.Context(), &awss3.GetObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String("null")}); err != nil || len(out.TagSet) != 0 || p.count() != 2 {
		t.Fatal("cleanup did not clear tags", out, err, p.count())
	}
}
