package s3gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const publicVersionTestKey = "key /+%.txt"
const publicVersionNativeOld = "provider-old/+%?"
const publicVersionNativeNew = "provider-new/+%?"

type publicVersionHTTPFixture struct {
	mu          sync.Mutex
	calls       int
	listCalls   int
	fault       string
	lastVersion string
}

func (p *publicVersionHTTPFixture) count() int { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }

func (p *publicVersionHTTPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	q := r.URL.Query()
	if r.Header.Get("Authorization") == "" && q.Get("X-Amz-Signature") == "" {
		t.Error("provider call was unsigned")
	}
	if q.Has("versions") {
		p.listCalls++
		p.list(t, w, r)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/physical/")
	if r.Method == http.MethodPut {
		w.Header().Set("ETag", `"written"`)
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			source, err := url.Parse(r.Header.Get("X-Amz-Copy-Source"))
			if err != nil || source.Path != "physical/"+publicVersionTestKey || source.Query().Get("versionId") != publicVersionNativeNew {
				t.Error("copy lost exact native source", source, err)
			}
			w.Header().Set("X-Amz-Version-Id", "provider-copy")
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;written&quot;</ETag><LastModified>2026-10-02T10:00:00Z</LastModified></CopyObjectResult>`)
		} else {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("X-Amz-Version-Id", "provider-put")
		}
		return
	}
	native := q.Get("versionId")
	p.lastVersion = native
	if native == "" {
		switch key {
		case publicVersionTestKey:
			native = publicVersionNativeNew
		case "put":
			native = "provider-put"
		case "copy":
			native = "provider-copy"
		case "null-key":
			native = "null"
		case "deleted":
			native = "provider-marker"
		}
	}
	knownVersion := key == publicVersionTestKey && (native == publicVersionNativeOld || native == publicVersionNativeNew) || key == "put" && native == "provider-put" || key == "copy" && native == "provider-copy" || key == "null-key" && native == "null" || key == "deleted" && native == "provider-marker"
	if !knownVersion {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchVersion</Code><Message>provider-private</Message></Error>`)
		return
	}
	if native == "provider-marker" {
		w.Header().Set("X-Amz-Version-Id", native)
		w.Header().Set("X-Amz-Delete-Marker", "true")
		w.Header().Set("Last-Modified", "Fri, 02 Oct 2026 10:00:00 GMT")
		if q.Has("versionId") {
			w.WriteHeader(405)
		} else {
			w.WriteHeader(404)
		}
		_, _ = io.WriteString(w, `<Error><Code>private-marker-error</Code><Message>physical private identity</Message></Error>`)
		return
	}
	body := "new-body"
	if native == publicVersionNativeOld {
		body = "old-body"
	}
	if native == "provider-put" {
		body = "data"
	}
	if native == "null" {
		body = "null-body"
	}
	w.Header().Set("X-Amz-Version-Id", native)
	w.Header().Set("ETag", `"etag"`)
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Last-Modified", "Fri, 02 Oct 2026 10:00:00 GMT")
	w.Header().Set("X-Amz-Meta-Owner", "customer")
	switch p.fault {
	case "wrong":
		w.Header().Set("X-Amz-Version-Id", "provider-wrong")
	case "missing":
		w.Header().Del("X-Amz-Version-Id")
	case "null-no-header":
		if native == "null" {
			w.Header().Del("X-Amz-Version-Id")
		}
	case "duplicate":
		w.Header().Add("X-Amz-Version-Id", "provider-wrong")
	case "malformed-marker":
		w.Header().Set("X-Amz-Delete-Marker", "maybe")
	case "not-found":
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchVersion</Code><Message>physical private</Message></Error>`)
		return
	case "denied":
		w.WriteHeader(403)
		return
	case "304":
		w.Header().Del("X-Amz-Version-Id")
		w.WriteHeader(304)
		return
	case "412":
		w.Header().Del("X-Amz-Version-Id")
		w.WriteHeader(412)
		return
	}
	status := 200
	if r.Header.Get("Range") == "bytes=1-3" {
		body = body[1:4]
		status = 206
		w.Header().Set("Content-Range", "bytes 1-3/8")
	}
	if status == http.StatusOK && (q.Get("X-Amz-Checksum-Mode") == "ENABLED" || r.Header.Get("X-Amz-Checksum-Mode") == "ENABLED") {
		w.Header().Set("X-Amz-Checksum-Sha256", publicVersionChecksum(body))
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, body)
	}
}

func (p *publicVersionHTTPFixture) list(t *testing.T, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("encoding-type") != "url" {
		t.Error("list did not ask for URL-safe provider keys", q)
	}
	w.Header().Set("Content-Type", "application/xml")
	if q.Get("key-marker") != "" {
		if q.Get("key-marker") != publicVersionTestKey || q.Get("version-id-marker") != "not-returned/+%?" {
			t.Error("paired continuation not resolved", q)
		}
		_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
		return
	}
	key := url.PathEscape(publicVersionTestKey)
	_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>not-returned/+%%?</NextVersionIdMarker><Version><Key>%s</Key><VersionId>%s</VersionId><IsLatest>false</IsLatest><Size>8</Size><ETag>&quot;old&quot;</ETag><LastModified>2026-10-02T09:00:00Z</LastModified></Version><Version><Key>%s</Key><VersionId>%s</VersionId><IsLatest>true</IsLatest><Size>8</Size><ETag>&quot;new&quot;</ETag><LastModified>2026-10-02T10:00:00Z</LastModified></Version><DeleteMarker><Key>deleted</Key><VersionId>provider-marker</VersionId><IsLatest>true</IsLatest><LastModified>2026-10-02T10:00:00Z</LastModified></DeleteMarker></ListVersionsResult>`, key, key, publicVersionNativeOld, key, publicVersionNativeNew)
}

// adr: 400
func TestPublicVersionsEndToEndMem(t *testing.T) { publicVersionsEndToEnd(t, state.NewMemStore()) }
func TestPublicVersionsEndToEndPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	publicVersionsEndToEnd(t, st)
}

func publicVersionsEndToEnd(t *testing.T, st multipartCopyIntegrationStore) {
	p := &publicVersionHTTPFixture{}
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	put, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("put"), Body: strings.NewReader("data")})
	if err != nil || !state.ValidObjectVersionID(aws.ToString(put.VersionId)) || aws.ToString(put.VersionId) == "provider-put" {
		t.Fatal(put, err)
	}
	versions, err := f.client.ListObjectVersions(t.Context(), &awss3.ListObjectVersionsInput{Bucket: aws.String("assets"), MaxKeys: aws.Int32(3), EncodingType: types.EncodingTypeUrl})
	if err != nil || len(versions.Versions) != 2 || len(versions.DeleteMarkers) != 1 || !aws.ToBool(versions.IsTruncated) || aws.ToString(versions.NextKeyMarker) != encodeListText(publicVersionTestKey, true) {
		t.Fatal(versions, err)
	}
	old, newID, markerID := aws.ToString(versions.Versions[0].VersionId), aws.ToString(versions.Versions[1].VersionId), aws.ToString(versions.DeleteMarkers[0].VersionId)
	for _, id := range []string{old, newID, markerID, aws.ToString(versions.NextVersionIdMarker)} {
		if !state.ValidObjectVersionID(id) || id == "null" {
			t.Fatal("private native ID exposed", id)
		}
	}
	get, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(old), Range: aws.String("bytes=1-3")})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(get.Body)
	_ = get.Body.Close()
	if err != nil || string(body) != "ld-" || aws.ToString(get.VersionId) != old || aws.ToString(get.ContentRange) != "bytes 1-3/8" || get.Metadata["owner"] != "customer" {
		t.Fatal(get, string(body), err)
	}
	head, err := f.client.HeadObject(t.Context(), &awss3.HeadObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(old), ChecksumMode: types.ChecksumModeEnabled})
	if err != nil || aws.ToString(head.VersionId) != old || aws.ToString(head.ChecksumSHA256) != publicVersionChecksum("old-body") {
		t.Fatal(head, err)
	}
	checked, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(old), ChecksumMode: types.ChecksumModeEnabled})
	if err != nil {
		t.Fatal(err)
	}
	checkedBody, err := io.ReadAll(checked.Body)
	_ = checked.Body.Close()
	if err != nil || string(checkedBody) != "old-body" || aws.ToString(checked.ChecksumSHA256) != publicVersionChecksum("old-body") {
		t.Fatal("version body failed SDK checksum validation", checked, err)
	}
	current, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey)})
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(current.Body)
	_ = current.Body.Close()
	if err != nil || string(body) != "new-body" || aws.ToString(current.VersionId) != newID {
		t.Fatal("current read did not share durable identity", current, string(body), err)
	}
	second, err := f.client.ListObjectVersions(t.Context(), &awss3.ListObjectVersionsInput{Bucket: aws.String("assets"), KeyMarker: aws.String(publicVersionTestKey), VersionIdMarker: versions.NextVersionIdMarker, MaxKeys: aws.Int32(3)})
	if err != nil || aws.ToBool(second.IsTruncated) || len(second.Versions) != 0 {
		t.Fatal(second, err)
	}
	before := p.count()
	for _, tc := range []struct{ key, id string }{{"other-key", old}, {publicVersionTestKey, uuid.NewString()}, {publicVersionTestKey, publicVersionNativeOld}} {
		_, err = f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(tc.key), VersionId: aws.String(tc.id)})
		assertSDKErrorCode(t, err, "NoSuchVersion")
	}
	if p.count() != before {
		t.Fatal("unknown or foreign versions reached provider")
	}
	_, err = f.client.ListObjectVersions(t.Context(), &awss3.ListObjectVersionsInput{Bucket: aws.String("assets"), KeyMarker: aws.String("other-key"), VersionIdMarker: aws.String(old)})
	assertSDKErrorCode(t, err, "InvalidArgument")
	if p.count() != before {
		t.Fatal("foreign continuation reached provider")
	}
	for _, specific := range []bool{false, true} {
		path := "/assets/deleted"
		want := 404
		if specific {
			path += "?versionId=" + markerID
			want = 405
		}
		response := rawPublicVersionRead(t, f, http.MethodGet, path)
		if response.Code != want || response.Header().Get("X-Amz-Version-Id") != markerID || response.Header().Get("X-Amz-Delete-Marker") != "true" || strings.Contains(response.Body.String(), "private") {
			t.Fatal("delete marker semantics", response)
		}
		if specific && response.Header().Get("Last-Modified") == "" {
			t.Fatal("specific marker lost modification date")
		}
	}
	for _, tc := range []struct {
		fault  string
		status int
	}{{"wrong", 503}, {"missing", 503}, {"duplicate", 503}, {"malformed-marker", 503}, {"not-found", 404}, {"denied", 503}, {"304", 304}, {"412", 412}} {
		p.mu.Lock()
		p.fault = tc.fault
		p.mu.Unlock()
		response := rawPublicVersionRead(t, f, http.MethodGet, "/assets/"+url.PathEscape(publicVersionTestKey)+"?versionId="+old)
		if response.Code != tc.status || strings.Contains(response.Body.String(), "old-body") || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Header().Get("X-Amz-Version-Id"), "provider") {
			t.Fatal(tc, response)
		}
		if (tc.status == 304 || tc.status == 412) && response.Header().Get("X-Amz-Version-Id") != old {
			t.Fatal("conditional result lost known version", response)
		}
	}
	p.mu.Lock()
	p.fault = ""
	p.mu.Unlock()
	null, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("null-key"), VersionId: aws.String("null")})
	if err != nil || aws.ToString(null.VersionId) != "null" {
		t.Fatal(null, err)
	}
	_ = null.Body.Close()
	p.mu.Lock()
	p.fault = "null-no-header"
	p.mu.Unlock()
	null, err = f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("null-key"), VersionId: aws.String("null")})
	if err != nil || aws.ToString(null.VersionId) != "null" {
		t.Fatal("unversioned null read required an immutable header", null, err)
	}
	_ = null.Body.Close()
	p.mu.Lock()
	p.fault = ""
	p.mu.Unlock()
	capStore := st.(state.ObjectCapacityStore)
	j, err := capStore.RequestObjectCapacityReconciliation(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = capStore.ClaimObjectCapacityReconciliation(t.Context(), j.ID, "public-native-baseline")
	if err != nil || j.InventoryScope != state.ObjectInventoryAllVersions {
		t.Fatal(j, err)
	}
	var inventory []state.ObjectVersionInventoryRecord
	for _, item := range []struct {
		key, native string
		size        int64
	}{{publicVersionTestKey, publicVersionNativeOld, 8}, {publicVersionTestKey, publicVersionNativeNew, 8}, {"put", "provider-put", 4}, {"deleted", "provider-marker", 7}, {"null-key", "null", 9}} {
		identity := sha256.Sum256([]byte(item.key + "\x00" + item.native))
		inventory = append(inventory, state.ObjectVersionInventoryRecord{Identity: hex.EncodeToString(identity[:]), Bytes: item.size})
	}
	j, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), j.ID, j.Token, "", inventory)
	if err != nil || j.State != "completed" || j.AfterBytes != 36 || j.AfterKeys != 5 {
		t.Fatal(j, err)
	}
	copy, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("copy"), CopySource: aws.String("assets/" + url.PathEscape(publicVersionTestKey))})
	if err != nil || !state.ValidObjectVersionID(aws.ToString(copy.VersionId)) {
		t.Fatal(copy, err)
	}
	resolved, err := st.(state.ObjectVersionReferenceStore).ResolveObjectVersion(t.Context(), f.bucket.AccountID, f.bucket.ID, "copy", aws.ToString(copy.VersionId))
	if err != nil || resolved != "provider-copy" {
		t.Fatal(resolved, err)
	}
}

func publicVersionChecksum(body string) string {
	sum := sha256.Sum256([]byte(body))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func rawPublicVersionRead(t *testing.T, f *multipartCopyIntegration, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+f.handler.host+path, nil)
	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	req.Header.Set("X-Amz-Content-Sha256", hash)
	if err := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, req, hash, "s3", "us-east-1", f.handler.now()); err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	f.handler.ServeHTTP(out, req)
	return out
}

type failingPublicVersionStore struct {
	Store
	state.ObjectVersionReferenceStore
}

func TestPublicVersionCredentialRevocationPreventsProviderAccess(t *testing.T) {
	st := state.NewMemStore()
	p := &publicVersionHTTPFixture{}
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	out, err := st.RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: publicVersionTestKey, ProviderVersionID: publicVersionNativeOld}})
	if err != nil {
		t.Fatal(err)
	}
	credential, _, err := st.ResolveObjectS3Credential(t.Context(), testAccess)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RevokeObjectS3Credential(t.Context(), f.bucket.AccountID, f.bucket.ID, credential.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets?versions", "/assets/" + url.PathEscape(publicVersionTestKey) + "?versionId=" + out[0].ID} {
		r := rawPublicVersionRead(t, f, http.MethodGet, path)
		if r.Code != http.StatusForbidden {
			t.Fatal(r)
		}
	}
	if p.count() != 0 {
		t.Fatal("revoked version credential reached provider")
	}
}

func (s failingPublicVersionStore) RecordObjectVersions(context.Context, string, string, []state.ObjectVersionIdentity) ([]state.ObjectVersionIdentity, error) {
	return nil, errors.New("database unavailable")
}

func TestPublicVersionReadMappingFailureHidesBody(t *testing.T) {
	p := &publicVersionHTTPFixture{}
	f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	f.handler.store = failingPublicVersionStore{Store: f.handler.store, ObjectVersionReferenceStore: f.store.(state.ObjectVersionReferenceStore)}
	r := rawPublicVersionRead(t, f, http.MethodGet, "/assets/"+url.PathEscape(publicVersionTestKey))
	if r.Code != 503 || strings.Contains(r.Body.String(), "new-body") || strings.Contains(r.Body.String(), "provider-new") || r.Header().Get("X-Amz-Version-Id") != "" {
		t.Fatal(r)
	}
}

func TestPublicVersionWriteOnlyCredentialCannotReadOrList(t *testing.T) {
	p := &publicVersionHTTPFixture{}
	f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }), state.ObjectBucketPermissionWrite)
	for _, path := range []string{"/assets?versions", "/assets/key?versionId=" + uuid.NewString()} {
		r := rawPublicVersionRead(t, f, http.MethodGet, path)
		if r.Code != 403 {
			t.Fatal(r)
		}
	}
	if p.count() != 0 {
		t.Fatal("read permission bypassed", p.count())
	}
}

func TestPublicVersionMaxKeysZeroAndRawXMLPrivacy(t *testing.T) {
	p := &publicVersionHTTPFixture{}
	f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	r := rawPublicVersionRead(t, f, http.MethodGet, "/assets?versions&max-keys=0")
	if r.Code != 200 || p.count() != 0 {
		t.Fatal(r, p.count())
	}
	r = rawPublicVersionRead(t, f, http.MethodGet, "/assets?versions&max-keys=3")
	var out listVersionsResult
	if r.Code != 200 || xml.Unmarshal(r.Body.Bytes(), &out) != nil || len(out.Versions) != 2 || strings.Contains(r.Body.String(), "provider-") || strings.Contains(r.Body.String(), "not-returned") || strings.Contains(r.Body.String(), "physical") {
		t.Fatal(r)
	}
	if out.Versions[0].LastModified != time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339Nano) {
		t.Fatal(out)
	}
}

var _ objectstorage.ObjectVersionLister = (*objectstorage.S3)(nil)
