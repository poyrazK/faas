package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type selectedCopyObject struct {
	key, version, body, owner, receipt, tags string
	size                                     int64
	marker                                   bool
}

type selectedCopyHTTPFixture struct {
	mu                   sync.Mutex
	objects              map[string]selectedCopyObject
	current              map[string]string
	parts                map[string]map[string]string
	calls, heads, copies int
	fault                string
	lastHead, lastCopy   string
}

func (p *selectedCopyHTTPFixture) setFault(fault string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fault = fault
}

func (p *selectedCopyHTTPFixture) counts() (calls, copies int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.copies
}

func newSelectedCopyHTTPFixture() *selectedCopyHTTPFixture {
	p := &selectedCopyHTTPFixture{objects: map[string]selectedCopyObject{}, current: map[string]string{}, parts: map[string]map[string]string{}}
	for _, o := range []selectedCopyObject{
		{key: publicVersionTestKey, version: publicVersionNativeOld, body: "old-body", size: 8, owner: "original", tags: "source=old"},
		{key: publicVersionTestKey, version: publicVersionNativeNew, body: "new-body", size: 8, owner: "changed"},
		{key: "large", version: "large-old/+?", body: "abcdefghij", size: 6 << 20, owner: "original"},
		{key: "null-key", version: "null", body: "null-body", size: 9, owner: "original"},
		{key: "null-key", version: "null-key-native-new", body: "changed!!", size: 9, owner: "changed"},
		{key: "deleted", version: "native-marker", size: int64(len("deleted")), marker: true},
	} {
		p.objects[o.key+"\x00"+o.version] = o
		p.current[o.key] = o.version
	}
	return p
}

func (p *selectedCopyHTTPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	q := r.URL.Query()
	if r.Header.Get("Authorization") == "" && q.Get("X-Amz-Signature") == "" {
		t.Error("unsigned provider call")
	}
	key := strings.TrimPrefix(r.URL.Path, "/physical/")
	switch {
	case q.Has("versions"):
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
		for _, o := range p.objects {
			if o.marker {
				_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>%s</Key><VersionId>%s</VersionId><IsLatest>true</IsLatest><LastModified>2026-10-02T09:00:00Z</LastModified></DeleteMarker>`, url.PathEscape(o.key), o.version)
			} else {
				_, _ = fmt.Fprintf(w, `<Version><Key>%s</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest><Size>%d</Size><ETag>&quot;source&quot;</ETag><LastModified>2026-10-02T09:00:00Z</LastModified></Version>`, url.PathEscape(o.key), o.version, p.current[o.key] == o.version, o.size)
			}
		}
		_, _ = io.WriteString(w, `</ListVersionsResult>`)
	case r.Method == http.MethodPost && q.Has("uploads"):
		id := fmt.Sprintf("private-upload-%d", len(p.parts)+1)
		p.parts[id] = map[string]string{}
		_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, id)
	case r.Method == http.MethodGet && q.Has("uploads"):
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		p.copy(t, w, r, key)
	case q.Get("uploadId") != "":
		p.multipart(t, w, r, key)
	case r.Method == http.MethodHead || r.Method == http.MethodGet:
		p.read(w, r, key)
	default:
		t.Errorf("unexpected provider method %s", r.Method)
		w.WriteHeader(500)
	}
}

func (p *selectedCopyHTTPFixture) read(w http.ResponseWriter, r *http.Request, key string) {
	version := r.URL.Query().Get("versionId")
	if r.Method == http.MethodHead {
		p.heads++
		p.lastHead = version
	}
	if version == "" {
		version = p.current[key]
	}
	o, exists := p.objects[key+"\x00"+version]
	if !exists || p.fault == "deleted-source" {
		w.WriteHeader(404)
		return
	}
	w.Header().Set("X-Amz-Version-Id", version)
	if o.marker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
		w.WriteHeader(405)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Amz-Meta-Owner", o.owner)
	if o.receipt != "" {
		w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, o.receipt)
	}
	w.Header().Set("ETag", `"source"`)
	w.Header().Set("Content-Length", strconv.FormatInt(o.size, 10))
	switch p.fault {
	case "wrong-head":
		w.Header().Set("X-Amz-Version-Id", "wrong-native")
	case "missing-head", "null-no-header":
		w.Header().Del("X-Amz-Version-Id")
	case "duplicate-head":
		w.Header().Add("X-Amz-Version-Id", "wrong-native")
	}
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, o.body)
	}
}

func (p *selectedCopyHTTPFixture) copy(t *testing.T, w http.ResponseWriter, r *http.Request, key string) {
	t.Helper()
	p.copies++
	path, rawQuery, _ := strings.Cut(r.Header.Get("X-Amz-Copy-Source"), "?")
	path, err := url.PathUnescape(path)
	q, qe := url.ParseQuery(rawQuery)
	sourceKey := strings.TrimPrefix(path, "physical/")
	version := q.Get("versionId")
	p.lastCopy = version
	o, exists := p.objects[sourceKey+"\x00"+version]
	if err != nil || qe != nil || !strings.HasPrefix(path, "physical/") || version == "" || !exists || o.marker {
		t.Error("copy failed to bind exact source", path, q)
		w.WriteHeader(404)
		return
	}
	if r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` && r.Header.Get("X-Amz-Copy-Source-If-Unmodified-Since") == "" {
		t.Error("copy lost source ETag fence")
	}
	if p.fault == "precondition" {
		w.WriteHeader(412)
		_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code><Message>private-provider-error</Message></Error>`)
		return
	}
	if id := r.URL.Query().Get("uploadId"); id != "" {
		if r.Header.Get("X-Amz-Copy-Source-Range") != "bytes=0-9" {
			t.Error("part range changed")
		}
		p.parts[id][r.URL.Query().Get("partNumber")] = o.body[:10]
	} else {
		o.key, o.version, o.receipt = key, fmt.Sprintf("copied-native-%d/+?", p.copies), r.Header.Get("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey)
		o.owner = r.Header.Get("X-Amz-Meta-Owner")
		if r.Header.Get("X-Amz-Tagging-Directive") == "REPLACE" {
			o.tags = r.Header.Get("X-Amz-Tagging")
		}
		p.objects[key+"\x00"+o.version], p.current[key] = o, o.version
		w.Header().Set("X-Amz-Version-Id", o.version)
	}
	w.Header().Set("X-Amz-Copy-Source-Version-Id", version)
	if p.fault == "wrong-copy" {
		w.Header().Set("X-Amz-Copy-Source-Version-Id", "wrong-native")
	}
	if p.fault == "lost-ack" {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
		return
	}
	result := "CopyObjectResult"
	if r.URL.Query().Get("uploadId") != "" {
		result = "CopyPartResult"
	}
	if p.fault == "truncated-ack" {
		_, _ = io.WriteString(w, `<`+result+`><ETag>`)
		return
	}
	_, _ = io.WriteString(w, `<`+result+`><ETag>&quot;source&quot;</ETag><LastModified>2026-10-02T10:00:00Z</LastModified></`+result+`>`)
}

func (p *selectedCopyHTTPFixture) multipart(t *testing.T, w http.ResponseWriter, r *http.Request, key string) {
	t.Helper()
	id := r.URL.Query().Get("uploadId")
	parts := p.parts[id]
	switch r.Method {
	case http.MethodGet:
		_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated>`)
		for part, body := range parts {
			_, _ = fmt.Fprintf(w, `<Part><PartNumber>%s</PartNumber><ETag>&quot;source&quot;</ETag><Size>%d</Size></Part>`, part, len(body))
		}
		_, _ = io.WriteString(w, `</ListPartsResult>`)
	case http.MethodPost:
		o := selectedCopyObject{key: key, version: "completed-native", body: parts["1"], size: int64(len(parts["1"]))}
		p.objects[key+"\x00"+o.version], p.current[key] = o, o.version
		delete(p.parts, id)
		_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;source&quot;</ETag></CompleteMultipartUploadResult>`)
	case http.MethodDelete:
		delete(p.parts, id)
		w.WriteHeader(204)
	default:
		t.Error("unexpected multipart method", r.Method)
		w.WriteHeader(500)
	}
}

func selectedCopySetup(t *testing.T, st multipartCopyIntegrationStore, permissions ...string) (*multipartCopyIntegration, *selectedCopyHTTPFixture, map[string]string) {
	t.Helper()
	p := newSelectedCopyHTTPFixture()
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }), permissions...)
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{
		{Key: publicVersionTestKey, ProviderVersionID: publicVersionNativeOld}, {Key: publicVersionTestKey, ProviderVersionID: publicVersionNativeNew},
		{Key: "large", ProviderVersionID: "large-old/+?"}, {Key: "null-key", ProviderVersionID: "null"}, {Key: "deleted", ProviderVersionID: "native-marker", DeleteMarker: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, ref := range refs {
		ids[ref.ProviderVersionID] = ref.ID
	}
	selectedCopyBaseline(t, f, p)
	return f, p, ids
}

func selectedCopyBaseline(t *testing.T, f *multipartCopyIntegration, p *selectedCopyHTTPFixture) {
	t.Helper()
	capacity := f.store.(state.ObjectCapacityStore)
	j, err := capacity.RequestObjectCapacityReconciliation(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = capacity.ClaimObjectCapacityReconciliation(t.Context(), j.ID, "selected-copy")
	if err != nil {
		t.Fatal(err)
	}
	var records []state.ObjectVersionInventoryRecord
	for identity, o := range p.objects {
		hash := sha256.Sum256([]byte(identity))
		records = append(records, state.ObjectVersionInventoryRecord{Identity: hex.EncodeToString(hash[:]), Bytes: o.size})
	}
	j, err = f.store.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), j.ID, j.Token, "", records)
	if err != nil || j.State != "completed" {
		t.Fatal(j, err)
	}
}

func selectedCopySource(key, id string) *string {
	return aws.String("assets/" + url.PathEscape(key) + "?versionId=" + url.QueryEscape(id))
}

// adr: 401
func TestSelectedCopyRestoreEndToEndMem(t *testing.T) {
	selectedCopyRestoreEndToEnd(t, state.NewMemStore())
}
func TestSelectedCopyRestoreEndToEndPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	selectedCopyRestoreEndToEnd(t, st)
}

func selectedCopyRestoreEndToEnd(t *testing.T, st multipartCopyIntegrationStore) {
	f, p, ids := selectedCopySetup(t, st)
	listed, err := f.client.ListObjectVersions(t.Context(), &awss3.ListObjectVersionsInput{Bucket: aws.String("assets")})
	if err != nil || len(listed.Versions) != 5 {
		t.Fatal(listed, err)
	}
	oldID := ids[publicVersionNativeOld]
	copy, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), CopySource: selectedCopySource(publicVersionTestKey, oldID)})
	if err != nil || aws.ToString(copy.CopySourceVersionId) != oldID || !state.ValidObjectVersionID(aws.ToString(copy.VersionId)) || aws.ToString(copy.VersionId) == oldID || aws.ToString(copy.VersionId) == ids[publicVersionNativeNew] {
		t.Fatal(copy, err)
	}
	for _, version := range []*string{nil, aws.String(oldID), copy.VersionId} {
		get, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: version})
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(get.Body)
		_ = get.Body.Close()
		if err != nil || string(body) != "old-body" || get.Metadata["owner"] != "original" {
			t.Fatal(string(body), get.Metadata, err)
		}
	}
	get, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(ids[publicVersionNativeNew])})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(get.Body)
	_ = get.Body.Close()
	if string(body) != "new-body" {
		t.Fatal("restore replaced retained version", string(body))
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].GrantedBytes != 8 || usage.Buckets[0].GrantedKeys != 1 {
		t.Fatal("restore did not charge new retained version", usage, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.copies != 1 || p.lastCopy != publicVersionNativeOld {
		t.Fatal("restore selected latest or replayed copy", p.copies, p.lastCopy)
	}
}

func TestSelectedPartCopyEndToEndMem(t *testing.T) { selectedPartCopyEndToEnd(t, state.NewMemStore()) }
func TestSelectedPartCopyEndToEndPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	selectedPartCopyEndToEnd(t, st)
}

func selectedPartCopyEndToEnd(t *testing.T, st multipartCopyIntegrationStore) {
	f, p, ids := selectedCopySetup(t, st)
	id := f.initiate(t, "destination")
	part, err := f.client.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), CopySource: selectedCopySource("large", ids["large-old/+?"]), CopySourceRange: aws.String("bytes=0-9")})
	if err != nil || aws.ToString(part.CopySourceVersionId) != ids["large-old/+?"] {
		t.Fatal(part, err)
	}
	listed, err := f.client.ListParts(t.Context(), &awss3.ListPartsInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id)})
	if err != nil || len(listed.Parts) != 1 || aws.ToInt64(listed.Parts[0].Size) != 10 {
		t.Fatal(listed, err)
	}
	_, err = f.client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.CopyPartResult.ETag}}}})
	if err != nil {
		t.Fatal(err)
	}
	get, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination")})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(get.Body)
	_ = get.Body.Close()
	if err != nil || string(body) != "abcdefghij" {
		t.Fatal(string(body), err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastHead != "large-old/+?" || p.lastCopy != "large-old/+?" || p.copies != 1 {
		t.Fatal("part did not retain selected source", p.lastHead, p.lastCopy, p.copies)
	}
}

func TestSelectedCopyIsolationMem(t *testing.T) { selectedCopyIsolation(t, state.NewMemStore()) }
func TestSelectedCopyIsolationPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	selectedCopyIsolation(t, st)
}

func selectedCopyIsolation(t *testing.T, st multipartCopyIntegrationStore) {
	f, p, ids := selectedCopySetup(t, st)
	id := f.initiate(t, "destination")
	for _, part := range []bool{false, true} {
		for _, source := range []*string{
			selectedCopySource(publicVersionTestKey, uuid.NewString()),
			selectedCopySource("other-key", ids[publicVersionNativeOld]),
			aws.String("other-bucket/" + url.PathEscape(publicVersionTestKey) + "?versionId=" + ids[publicVersionNativeOld]),
			selectedCopySource(publicVersionTestKey, publicVersionNativeOld),
			aws.String("assets/key?versionId=" + ids[publicVersionNativeOld] + "&versionId=null"),
		} {
			p.mu.Lock()
			before := p.calls
			p.mu.Unlock()
			var err error
			if part {
				_, err = f.client.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), CopySource: source})
			} else {
				_, err = f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: source})
			}
			if err == nil {
				t.Fatal("unowned source accepted", *source)
			}
			p.mu.Lock()
			after := p.calls
			p.mu.Unlock()
			if after != before {
				t.Fatal("unowned selector reached provider", *source, before, after)
			}
		}
	}
}

func TestSelectedCopyNullAndSourceFailures(t *testing.T) {
	for _, tc := range []struct{ name, key, native, fault, code string }{
		{"null data", "null-key", "null", "", ""},
		{"unversioned null data", "null-key", "null", "null-no-header", ""},
		{"selected delete marker", "deleted", "native-marker", "", "InvalidRequest"},
		{"missing head identity", publicVersionTestKey, publicVersionNativeOld, "missing-head", "ServiceUnavailable"},
		{"wrong head identity", publicVersionTestKey, publicVersionNativeOld, "wrong-head", "ServiceUnavailable"},
		{"duplicate head identity", publicVersionTestKey, publicVersionNativeOld, "duplicate-head", "ServiceUnavailable"},
		{"deleted source", publicVersionTestKey, publicVersionNativeOld, "deleted-source", "NoSuchKey"},
		{"provider precondition", publicVersionTestKey, publicVersionNativeOld, "precondition", "PreconditionFailed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p, ids := selectedCopySetup(t, state.NewMemStore())
			p.setFault(tc.fault)
			out, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: selectedCopySource(tc.key, ids[tc.native])})
			if tc.code != "" {
				assertSDKErrorCode(t, err, tc.code)
			} else if err != nil || aws.ToString(out.CopySourceVersionId) != "null" {
				t.Fatal(out, err)
			}
			if tc.code == "" {
				p.setFault("")
				get, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), VersionId: out.VersionId})
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(get.Body)
				_ = get.Body.Close()
				if err != nil || string(body) != "null-body" {
					t.Fatal("null copy selected newer current version", string(body), err)
				}
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if tc.code != "" && tc.code != "PreconditionFailed" && p.copies != 0 {
				t.Fatal("invalid source reached copy dispatch")
			}
			if p.lastHead != tc.native {
				t.Fatal("source inspection was not version-specific", p.lastHead)
			}
		})
	}
}

func TestSelectedCopyCredentialPermissionsAndRevocation(t *testing.T) {
	for _, permission := range []string{state.ObjectBucketPermissionRead, state.ObjectBucketPermissionWrite, state.ObjectBucketPermissionReadWrite} {
		t.Run(permission, func(t *testing.T) {
			st := state.NewMemStore()
			f, p, ids := selectedCopySetup(t, st, permission)
			if permission == state.ObjectBucketPermissionReadWrite {
				credential, _, err := st.ResolveObjectS3Credential(t.Context(), testAccess)
				if err != nil {
					t.Fatal(err)
				}
				if err = st.RevokeObjectS3Credential(t.Context(), f.bucket.AccountID, f.bucket.ID, credential.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: selectedCopySource(publicVersionTestKey, ids[publicVersionNativeOld])})
			code := "AccessDenied"
			if permission == state.ObjectBucketPermissionReadWrite {
				code = "InvalidAccessKeyId"
			}
			assertSDKErrorCode(t, err, code)
			if calls, _ := p.counts(); calls != 0 {
				t.Fatal("unauthorized selector reached provider")
			}
		})
	}
}

func TestSelectedCopyMappingFailureBeforeDispatch(t *testing.T) {
	f, p, ids := selectedCopySetup(t, state.NewMemStore())
	f.handler.store = failingPublicVersionStore{Store: f.handler.store, ObjectVersionReferenceStore: f.store.(state.ObjectVersionReferenceStore)}
	_, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: selectedCopySource(publicVersionTestKey, ids[publicVersionNativeOld])})
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	if _, copies := p.counts(); copies != 0 {
		t.Fatal("mapping failure reached write dispatch")
	}
}

func TestSelectedCopyQuotaAdmission(t *testing.T) {
	f, p, ids := selectedCopySetup(t, state.NewMemStore())
	usage, err := f.store.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.handler.registry.Accounting.MaxBucketBytes = usage.Buckets[0].ObservedBytes + 7
	_, err = f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), CopySource: selectedCopySource(publicVersionTestKey, ids[publicVersionNativeOld])})
	assertSDKErrorCode(t, err, "OperationAborted")
	if _, copies := p.counts(); copies != 0 {
		t.Fatal("restore escaped quota")
	}
}

func TestSelectedCopyMetadataAndTaggingDirectives(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(strconv.FormatBool(replace), func(t *testing.T) {
			f, p, ids := selectedCopySetup(t, state.NewMemStore())
			in := &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: selectedCopySource(publicVersionTestKey, ids[publicVersionNativeOld])}
			wantOwner, wantTags := "original", "source=old"
			if replace {
				in.MetadataDirective, in.TaggingDirective = types.MetadataDirectiveReplace, types.TaggingDirectiveReplace
				in.Metadata, in.Tagging = map[string]string{"owner": "replacement"}, aws.String("destination=new")
				wantOwner, wantTags = "replacement", "destination=new"
			}
			out, err := f.client.CopyObject(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			o := p.objects["destination\x00"+p.current["destination"]]
			if o.owner != wantOwner || o.tags != wantTags || aws.ToString(out.CopySourceVersionId) != ids[publicVersionNativeOld] {
				t.Fatal("selected-version directives changed", o, out)
			}
		})
	}
}

func TestSelectedCopyUncertainAcknowledgmentPG(t *testing.T) {
	for _, part := range []bool{false, true} {
		for _, fault := range []string{"lost-ack", "truncated-ack", "wrong-copy"} {
			t.Run(strconv.FormatBool(part)+"/"+fault, func(t *testing.T) {
				st, pool := multipartCopyPGStore(t)
				f, p, ids := selectedCopySetup(t, st)
				var id string
				if part {
					id = f.initiate(t, "destination")
				}
				p.setFault(fault)
				var err error
				if part {
					_, err = f.client.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), CopySource: selectedCopySource("large", ids["large-old/+?"]), CopySourceRange: aws.String("bytes=0-9")})
				} else {
					_, err = f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), CopySource: selectedCopySource(publicVersionTestKey, ids[publicVersionNativeOld])})
				}
				assertSDKErrorCode(t, err, "ServiceUnavailable")
				restarted := state.NewPgStore(pool)
				if part {
					err = restarted.BeginObjectMultipartPart(t.Context(), f.bucket.AccountID, f.bucket.ID, id, "restart", 1, 10, f.handler.registry.MaxUploadBytes, f.handler.registry.Accounting)
					if !errors.Is(err, state.ErrConflict) {
						t.Fatal("restart lost transfer fence", err)
					}
				} else {
					receipts, err := restarted.ListObjectWriteReceipts(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, "pending", 10, "")
					if err != nil || len(receipts.Items) != 1 || receipts.Items[0].Bytes != 8 || receipts.Items[0].Operation != "copy" {
						t.Fatal("restart lost uncertain restore", receipts, err)
					}
					p.setFault("")
					backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
					if err != nil {
						t.Fatal(err)
					}
					proof, err := backend.Provider.(objectstorage.ObjectWriteConfirmer).ConfirmTrackedObject(t.Context(), f.bucket.PhysicalName, publicVersionTestKey, receipts.Items[0].ID, 8)
					if err != nil || proof.ProviderVersionID == "" {
						t.Fatal("committed restore lacked exact recovery proof", proof, err)
					}
				}
				usage, err := restarted.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
				if err != nil || part && usage.Buckets[0].MultipartBytes != 10 || !part && usage.Buckets[0].GrantedBytes != 8 {
					t.Fatal("uncertain copy refunded reservation", usage, err)
				}
				if _, copies := p.counts(); copies != 1 {
					t.Fatal("uncertain copy replayed", copies)
				}
			})
		}
	}
}
