package journeys

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// GA inventory row 108: managed object storage. It is still a preview
// capability released per account, so the journey first checks that the
// account may use it; until it may, that step is a known failure
// (known_failures.json) and the rest report blocked.

// objectBucket names the bucket of both of the journey's apps. Names are
// unique only within an app and scope, so the second app's same-named
// bucket stands in for another tenant's.
const objectBucket = "journey-data"

// The objects the journey writes. unicodeKey needs escaping in every URL
// and signature.
const (
	unicodeKey   = "e2e/ünïcødé dir/naïve file ☃.txt"
	emptyKey     = "e2e/empty.txt"
	sdkKey       = "e2e/sdk/hello.txt"
	multipartKey = "e2e/sdk/large.bin"
	copyKey      = "e2e/sdk/copy of héllo.txt"
)

// journeyBucket is a bucket the journey creates and what its cleanup needs.
type journeyBucket struct {
	app, id     string
	credentials []string // S3 credentials to revoke before deleting it
	deleted     bool
}

func (b *journeyBucket) path() string { return "/v1/apps/" + b.app + "/buckets/" + b.id }

// s3Credential is a bucket-scoped S3 credential; the API shows its secret
// once.
type s3Credential struct {
	id, accessKey, secret, endpoint, region, addressing string
}

// objectJourney is the state TestObjectStorage's steps share.
type objectJourney struct {
	tg           *target
	key          string // API key for the calls the CLI has no command for
	ours, theirs *journeyBucket
	cred         s3Credential
	client       *s3.Client
	written      map[string][]byte // objects the bucket holds, by key
}

// TestObjectStorage is a customer's object-storage journey (row 108): add a
// bucket to an app and list it, move objects through signed URLs and an AWS
// S3 SDK on the branded endpoint, check that another tenant's credential, a
// forged signature, an unsigned request and a revoked credential reach
// nothing, then empty and delete the bucket. Versioning, Object Lock,
// lifecycle rules, notifications and CORS rules stay preview and are not run.
func TestObjectStorage(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &objectJourney{tg: tg, written: map[string][]byte{}}
	j.ours, j.theirs = &journeyBucket{app: tg.app(t, "objects")}, &journeyBucket{app: tg.app(t, "objects-other")}
	j.key = tg.scopedKey(t, "storage")
	// Gregale refuses to delete an app that still has buckets, so the
	// buckets go first (cleanups run last-registered first).
	t.Cleanup(func() {
		tg.removeBucket(t, j.key, j.theirs)
		tg.removeBucket(t, j.key, j.ours)
	})
	s := newStory(t)
	s.step("object_storage_enabled", j.enabled)
	s.step("create_bucket", func(t *testing.T) { tg.addBucket(t, j.ours) })
	s.check("list_bucket", j.listBucket)
	s.check("retried_create_keeps_one_bucket", j.retriedCreate)
	s.step("s3_credential", j.mintCredential)
	s.check("signed_url_unicode_key", j.signedRoundTrip(unicodeKey, []byte("Grüße through a signed URL ✓\n")))
	s.check("signed_url_empty_object", j.signedRoundTrip(emptyKey, nil))
	s.check("s3_put_get", j.putGet)
	s.check("s3_multipart_upload", j.multipart)
	s.check("s3_copy_object", j.copyObject)
	s.check("s3_list_objects", j.listObjects)
	s.check("s3_delete_object", j.deleteObject)
	s.check("other_tenant_credential_denied", j.otherTenantDenied)
	s.check("forged_signature_denied", j.forgedDenied)
	s.check("unsigned_request_denied", j.unsignedDenied)
	s.check("delete_non_empty_bucket_refused", j.deleteRefused)
	s.step("empty_bucket", j.emptyBucket)
	s.check("revoked_credential_denied", j.revokedDenied)
	s.step("delete_bucket", j.deleteBucket)
}

// enabled checks that the account may use object storage: the preview
// capability needs the installation's s3_enabled and a release to the
// account, and `gregale feature list` reports both.
func (j *objectJourney) enabled(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "feature", "list").stdout)
	list, _ := field(doc, "capabilities").([]any)
	for _, c := range list {
		if str(c, "key") != "object-storage" {
			continue
		}
		if str(c, "enabled") != "true" {
			t.Fatalf("object storage is not enabled for this account (%s): %s",
				str(c, "unavailable_reason"), str(c, "unavailable_detail"))
		}
		return
	}
	t.Fatal("gregale feature list does not list object-storage")
}

func (j *objectJourney) listBucket(t *testing.T) {
	list := object(t, j.tg.ok(t, time.Minute, "bucket", "list", j.ours.app).stdout)
	if str(list, "enabled") != "true" {
		t.Errorf("bucket list says object storage is not enabled: %s", compact(list))
	}
	if ids := bucketIDs(list); len(ids) != 1 || ids[0] != j.ours.id {
		t.Fatalf("bucket list shows %v named %s, want [%s]: %s", ids, objectBucket, j.ours.id, compact(list))
	}
}

// retriedCreate repeats the create through the CLI and the API: both must
// return the existing bucket, never a second one.
func (j *objectJourney) retriedCreate(t *testing.T) {
	again := object(t, j.tg.ok(t, 6*time.Minute, "add", "bucket", objectBucket, "--app", j.ours.app, "--env", "default").stdout)
	if str(again, "bucket.id") != j.ours.id || str(again, "bucket_created") != "false" || str(again, "binding_created") != "false" {
		t.Errorf("a repeated add bucket did not reuse bucket %s and its binding: %s", j.ours.id, compact(again))
	}
	r := j.tg.apiCall(http.MethodPost, "/v1/apps/"+j.ours.app+"/buckets",
		map[string]string{"name": objectBucket, "scope": "default"}, j.key)
	if r.status != http.StatusOK || str(jsonBody(t, r), "id") != j.ours.id {
		t.Errorf("a retried POST /buckets did not return bucket %s: %s", j.ours.id, r)
	}
	list := object(t, j.tg.ok(t, time.Minute, "bucket", "list", j.ours.app).stdout)
	if ids := bucketIDs(list); len(ids) != 1 {
		t.Errorf("after the retries bucket list shows %d buckets named %s: %v", len(ids), objectBucket, ids)
	}
}

func (j *objectJourney) mintCredential(t *testing.T) {
	j.cred = j.tg.mintS3Credential(t, j.key, j.ours)
	if want := j.tg.s3Endpoint(); strings.TrimRight(j.cred.endpoint, "/") != want || j.cred.addressing != "path" || j.cred.region == "" {
		t.Fatalf("the S3 credential points at %s (region %q, %q addressing), want %s with path-style addressing",
			j.cred.endpoint, j.cred.region, j.cred.addressing, want)
	}
	j.client = s3Client(j.cred)
}

// signedRoundTrip writes an object through a signed PUT URL and reads it
// back through a signed GET URL.
func (j *objectJourney) signedRoundTrip(objectKey string, body []byte) func(*testing.T) {
	return func(t *testing.T) {
		if put := j.tg.signedTransfer(t, j.key, j.ours, http.MethodPut, objectKey, body); put.status != http.StatusOK {
			t.Fatalf("signed PUT of %q: %s", objectKey, put)
		}
		j.written[objectKey] = body
		got := j.tg.signedTransfer(t, j.key, j.ours, http.MethodGet, objectKey, nil)
		if got.status != http.StatusOK || !bytes.Equal(got.body, body) {
			t.Fatalf("signed GET of %q returned %d bytes, want %d: %s", objectKey, len(got.body), len(body), got)
		}
	}
}

func (j *objectJourney) putGet(t *testing.T) {
	body := []byte("written with the AWS SDK in run " + j.tg.runID + "\n")
	_, err := j.client.PutObject(opContext(t), &s3.PutObjectInput{
		Bucket: aws.String(objectBucket), Key: aws.String(sdkKey),
		Body: bytes.NewReader(body), ContentType: aws.String("text/plain; charset=utf-8"),
	})
	if err != nil {
		t.Fatalf("PutObject %q: %v", sdkKey, err)
	}
	j.written[sdkKey] = body
	if got := getObject(t, j.client, sdkKey); !bytes.Equal(got, body) {
		t.Fatalf("GetObject %q returned %q, want %q", sdkKey, got, body)
	}
}

// multipart uploads an object over 5 MiB in two parts (S3 requires every
// part but the last to be at least 5 MiB) and reads it back whole.
func (j *objectJourney) multipart(t *testing.T) {
	ctx := opContext(t)
	start := time.Now()
	created, err := j.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(objectBucket), Key: aws.String(multipartKey), ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	completed := false
	defer func() {
		if !completed {
			abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			_, _ = j.client.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{
				Bucket: aws.String(objectBucket), Key: aws.String(multipartKey), UploadId: created.UploadId,
			})
		}
	}()
	var whole []byte
	var parts []types.CompletedPart
	var number int32
	for _, part := range [][]byte{randomBytes(t, 5<<20), randomBytes(t, 512<<10)} {
		number++
		out, err := j.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket: aws.String(objectBucket), Key: aws.String(multipartKey), UploadId: created.UploadId,
			PartNumber: aws.Int32(number), Body: bytes.NewReader(part), ContentLength: aws.Int64(int64(len(part))),
		})
		if err != nil {
			t.Fatalf("UploadPart %d: %v", number, err)
		}
		parts = append(parts, types.CompletedPart{ETag: out.ETag, PartNumber: aws.Int32(number)})
		whole = append(whole, part...)
	}
	_, err = j.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(objectBucket), Key: aws.String(multipartKey), UploadId: created.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	completed = true
	j.written[multipartKey] = whole
	t.Logf("metric object_multipart_upload_s=%.1f", time.Since(start).Seconds())
	if got := getObject(t, j.client, multipartKey); sha256.Sum256(got) != sha256.Sum256(whole) {
		t.Fatalf("the multipart object reads back as %d bytes that differ from the %d uploaded", len(got), len(whole))
	}
}

func (j *objectJourney) copyObject(t *testing.T) {
	source, ok := j.written[sdkKey]
	if !ok {
		t.Skip("needs the object s3_put_get writes")
	}
	_, err := j.client.CopyObject(opContext(t), &s3.CopyObjectInput{
		Bucket: aws.String(objectBucket), Key: aws.String(copyKey),
		CopySource: aws.String(objectBucket + "/" + url.PathEscape(sdkKey)),
	})
	if err != nil {
		t.Fatalf("CopyObject %q to %q: %v", sdkKey, copyKey, err)
	}
	j.written[copyKey] = source
	if got := getObject(t, j.client, copyKey); !bytes.Equal(got, source) {
		t.Fatalf("the copy reads %q, want %q", got, source)
	}
}

// listObjects pages ListObjectsV2 two keys at a time and checks that every
// written object is listed at its size, Unicode and empty keys included.
func (j *objectJourney) listObjects(t *testing.T) {
	if len(j.written) == 0 {
		t.Skip("no object was written")
	}
	listed, err := listKeys(t, j.client, "e2e/")
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	for key, body := range j.written {
		if size, ok := listed[key]; !ok {
			t.Errorf("ListObjectsV2 does not list %q; it lists %v", key, sortedKeys(listed))
		} else if size != int64(len(body)) {
			t.Errorf("ListObjectsV2 lists %q at %d bytes, want %d", key, size, len(body))
		}
	}
}

func (j *objectJourney) deleteObject(t *testing.T) {
	if _, ok := j.written[copyKey]; !ok {
		t.Skip("needs the copy s3_copy_object writes")
	}
	if _, err := j.client.DeleteObject(opContext(t), &s3.DeleteObjectInput{Bucket: aws.String(objectBucket), Key: aws.String(copyKey)}); err != nil {
		t.Fatalf("DeleteObject %q: %v", copyKey, err)
	}
	delete(j.written, copyKey)
	if err := getError(t, j.client, copyKey); s3ErrorCode(err) != "NoSuchKey" {
		t.Fatalf("GetObject after DeleteObject: want NoSuchKey, got %v", err)
	}
}

// otherTenantDenied gives the second app a bucket of the same name and an
// S3 credential for it. That credential must reach only its own empty
// bucket, never this one's objects. The suite runs as one account, so
// another app stands in for another tenant: a credential belongs to one
// bucket, whoever owns it.
func (j *objectJourney) otherTenantDenied(t *testing.T) {
	if len(j.written) == 0 {
		t.Skip("no object was written to look for")
	}
	j.tg.addBucket(t, j.theirs)
	if j.theirs.id == j.ours.id {
		t.Fatalf("the second app's bucket has this app's bucket ID %s", j.ours.id)
	}
	other := s3Client(j.tg.mintS3Credential(t, j.key, j.theirs))
	for key := range j.written {
		if err := getError(t, other, key); err == nil {
			t.Errorf("the other app's credential read %q of bucket %s", key, j.ours.id)
		}
	}
	listed, err := listKeys(t, other, "")
	if err != nil {
		t.Fatalf("the other app's credential cannot list its own bucket: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("the other app's credential lists %v, want its own empty bucket", sortedKeys(listed))
	}
}

// forgedDenied signs a request with this bucket's access key and a wrong
// secret.
func (j *objectJourney) forgedDenied(t *testing.T) {
	forged := j.cred
	forged.secret = randomHex(t, 40)
	if err := getError(t, s3Client(forged), sdkKey); s3ErrorCode(err) != "SignatureDoesNotMatch" {
		t.Fatalf("a request signed with a wrong secret: want SignatureDoesNotMatch, got %v", err)
	}
}

// unsignedDenied lists the bucket, reads an object and overwrites it with
// plain unsigned requests: none may succeed.
func (j *objectJourney) unsignedDenied(t *testing.T) {
	bucketURL := j.tg.s3Endpoint() + "/" + objectBucket
	objectURL := bucketURL + (&url.URL{Path: "/" + sdkKey}).EscapedPath()
	for what, r := range map[string]httpResult{
		"list":  get(bucketURL),
		"read":  get(objectURL),
		"write": send(http.MethodPut, objectURL, map[string]string{"Content-Type": "text/plain"}, []byte("overwritten without a signature\n")),
	} {
		if r.status != http.StatusForbidden {
			t.Errorf("an unsigned %s answered %s, want 403", what, r)
		}
	}
	if body, ok := j.written[sdkKey]; ok {
		if got := getObject(t, j.client, sdkKey); !bytes.Equal(got, body) {
			t.Errorf("after the unsigned PUT %q reads %q, want %q", sdkKey, got, body)
		}
	}
}

// deleteRefused tries to delete the bucket while it holds objects: Gregale
// refuses, and the app keeps its binding to the bucket.
func (j *objectJourney) deleteRefused(t *testing.T) {
	if len(j.written) == 0 {
		t.Skip("the bucket holds no object")
	}
	res := j.tg.run(t, 2*time.Minute, "bucket", "delete", j.ours.app, j.ours.id, "--yes")
	if res.exit == 0 {
		t.Fatalf("bucket delete removed a bucket holding %d objects", len(j.written))
	}
	if !strings.Contains(res.problem(), "bucket_not_empty") {
		t.Errorf("deleting a non-empty bucket failed with %s, want bucket_not_empty", res.problem())
	}
	r := j.tg.apiCall(http.MethodGet, j.ours.path()+"/compute-bindings", nil, j.key)
	if r.status != http.StatusOK {
		t.Fatalf("list the bucket's bindings: %s", r)
	}
	if items, _ := field(jsonBody(t, r), "items").([]any); len(items) != 1 {
		t.Errorf("after the refused delete the app has %d bindings to the bucket, want 1", len(items))
	}
}

// emptyBucket deletes every object with DeleteObject and checks that the
// bucket lists empty.
func (j *objectJourney) emptyBucket(t *testing.T) {
	listed, err := listKeys(t, j.client, "")
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	for key := range listed {
		if _, err := j.client.DeleteObject(opContext(t), &s3.DeleteObjectInput{Bucket: aws.String(objectBucket), Key: aws.String(key)}); err != nil {
			t.Fatalf("DeleteObject %q: %v", key, err)
		}
		delete(j.written, key)
	}
	if left, err := listKeys(t, j.client, ""); err != nil || len(left) != 0 {
		t.Fatalf("after deleting every object the bucket lists %v (%v)", sortedKeys(left), err)
	}
}

// revokedDenied revokes the S3 credential; its next request must fail.
func (j *objectJourney) revokedDenied(t *testing.T) {
	r := j.tg.apiCall(http.MethodDelete, j.ours.path()+"/s3-credentials/"+j.cred.id, nil, j.key)
	if r.status != http.StatusNoContent && r.status != http.StatusOK {
		t.Fatalf("revoke the S3 credential: %s", r)
	}
	eventually(t, "the revoked credential is refused", 30*time.Second, 2*time.Second, func() (bool, string) {
		_, err := j.client.ListObjectsV2(opContext(t), &s3.ListObjectsV2Input{Bucket: aws.String(objectBucket)})
		return s3ErrorCode(err) == "InvalidAccessKeyId", fmt.Sprint(err)
	})
}

func (j *objectJourney) deleteBucket(t *testing.T) {
	j.tg.ok(t, 2*time.Minute, "bucket", "delete", j.ours.app, j.ours.id, "--yes")
	j.ours.deleted = true
	eventually(t, "the bucket leaves bucket list", time.Minute, 3*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "bucket", "list", j.ours.app)
		docs, err := decodeObjects(res.stdout)
		if res.exit != 0 || err != nil || len(docs) != 1 {
			return false, res.problem()
		}
		ids := bucketIDs(docs[0])
		return len(ids) == 0, fmt.Sprint(ids)
	})
}

// --- helpers -------------------------------------------------------------

// addBucket creates b's app without deploying it, as the object-storage
// templates do, then a ready bucket bound to it.
func (tg *target) addBucket(t *testing.T, b *journeyBucket) {
	t.Helper()
	if res := tg.exec(t, tg.token, t.TempDir(), time.Minute, "deploy", "--create-only", "--app", "--name", b.app); res.exit != 0 {
		t.Fatalf("deploy --create-only %s failed (exit %d): %s", b.app, res.exit, tg.redact(res.problem()))
	}
	doc := object(t, tg.ok(t, 6*time.Minute, "add", "bucket", objectBucket, "--app", b.app, "--env", "default").stdout)
	b.id = str(doc, "bucket.id")
	if b.id == "" || str(doc, "bucket.state") != "ready" {
		t.Fatalf("add bucket did not create a ready bucket: %s", compact(doc))
	}
}

// bucketIDs lists the IDs of a bucket list's live buckets named objectBucket.
func bucketIDs(list map[string]any) []string {
	items, _ := field(list, "items").([]any)
	var ids []string
	for _, item := range items {
		if str(item, "name") == objectBucket && str(item, "state") != "deleted" {
			ids = append(ids, str(item, "id"))
		}
	}
	return ids
}

// mintS3Credential creates a read_write S3 credential for b through the API
// (the CLI has no command for it) and records it for revocation. Neither
// the response nor its secret is ever logged.
func (tg *target) mintS3Credential(t *testing.T, key string, b *journeyBucket) s3Credential {
	t.Helper()
	r := tg.apiCall(http.MethodPost, b.path()+"/s3-credentials",
		map[string]string{"label": tg.slug("s3"), "permission": "read_write"}, key)
	if r.status != http.StatusCreated {
		t.Fatalf("create an S3 credential for bucket %s: HTTP %d %s", b.id, r.status, apiProblem(r))
	}
	var c struct {
		ID              string `json:"id"`
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		Endpoint        string `json:"endpoint"`
		Region          string `json:"region"`
		AddressingStyle string `json:"addressing_style"`
	}
	err := json.Unmarshal(r.body, &c)
	if c.ID != "" {
		b.credentials = append(b.credentials, c.ID)
	}
	if err != nil || c.ID == "" || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		t.Fatalf("the S3 credential response has no id, access key or secret (decode: %v)", err)
	}
	return s3Credential{id: c.ID, accessKey: c.AccessKeyID, secret: c.SecretAccessKey,
		endpoint: c.Endpoint, region: c.Region, addressing: c.AddressingStyle}
}

// s3Endpoint is the branded S3 endpoint beside the target's API, e.g.
// https://s3.gregale.dev for https://api.gregale.dev.
func (tg *target) s3Endpoint() string {
	u, err := url.Parse(tg.api)
	if err != nil {
		return ""
	}
	return "https://s3." + strings.TrimPrefix(u.Hostname(), "api.")
}

// s3Client is an AWS SDK client configured as docs/object-storage.md tells
// customers: the credential's endpoint and region, path-style addressing.
// Flexible checksums depend on the bucket's storage provider, so it sends
// them only where S3 requires them, like Gregale's own binding probe.
func s3Client(c s3Credential) *s3.Client {
	return s3.New(s3.Options{
		Region:                     c.region,
		BaseEndpoint:               aws.String(c.endpoint),
		UsePathStyle:               true,
		Credentials:                credentials.NewStaticCredentialsProvider(c.accessKey, c.secret, ""),
		HTTPClient:                 &http.Client{Timeout: 2 * time.Minute},
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
}

// signedTransfer asks the API for a signed URL and sends it with a plain
// HTTP client, as a browser would: the API key never travels with it. The
// URL is a credential, so only its host is logged.
func (tg *target) signedTransfer(t *testing.T, key string, b *journeyBucket, method, objectKey string, body []byte) httpResult {
	t.Helper()
	request := map[string]any{"method": method, "key": objectKey, "expires_in": 300}
	if method == http.MethodPut {
		request["size_bytes"] = len(body)
		request["content_type"] = "text/plain; charset=utf-8"
	}
	r := tg.apiCall(http.MethodPost, b.path()+"/signed-url", request, key)
	if r.status != http.StatusOK {
		t.Fatalf("sign %s %q: HTTP %d %s", method, objectKey, r.status, apiProblem(r))
	}
	var signed struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(r.body, &signed); err != nil {
		t.Fatalf("decode the signed %s URL: %v", method, err)
	}
	u, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatalf("the signed %s URL does not parse: %v", method, err)
	}
	if u.Scheme+"://"+u.Host != tg.s3Endpoint() {
		t.Fatalf("the signed %s URL is on %s://%s, want %s", method, u.Scheme, u.Host, tg.s3Endpoint())
	}
	return send(signed.Method, signed.URL, signed.Headers, body)
}

// apiProblem is an API error's code and detail, never a success body.
func apiProblem(r httpResult) string {
	var p struct{ Code, Detail string }
	if r.err != nil {
		return r.err.Error()
	}
	if json.Unmarshal(r.body, &p) != nil || p.Code == "" {
		return "(no problem details)"
	}
	return p.Code + ": " + p.Detail
}

// getObject reads a whole object and fails the test on any error.
func getObject(t *testing.T, client *s3.Client, key string) []byte {
	t.Helper()
	out, err := client.GetObject(opContext(t), &s3.GetObjectInput{Bucket: aws.String(objectBucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject %q: %v", key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return body
}

// getError is GetObject's error, nil when the read succeeded.
func getError(t *testing.T, client *s3.Client, key string) error {
	t.Helper()
	out, err := client.GetObject(opContext(t), &s3.GetObjectInput{Bucket: aws.String(objectBucket), Key: aws.String(key)})
	if err == nil {
		_ = out.Body.Close()
	}
	return err
}

// listKeys pages ListObjectsV2 under prefix, two keys a page so the
// continuation token is exercised, and returns each key's size.
func listKeys(t *testing.T, client *s3.Client, prefix string) (map[string]int64, error) {
	t.Helper()
	keys := map[string]int64{}
	pages := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(objectBucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(2),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(opContext(t))
		if err != nil {
			return keys, err
		}
		for _, o := range page.Contents {
			keys[aws.ToString(o.Key)] = aws.ToInt64(o.Size)
		}
	}
	return keys, nil
}

func s3ErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// opContext bounds one S3 call.
func opContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return b
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// removeBucket deletes a bucket the journey created, emptying it and
// revoking its S3 credentials first: Gregale refuses to delete an app that
// still has buckets. Problems are warnings, like the apps' own cleanup.
func (tg *target) removeBucket(t *testing.T, key string, b *journeyBucket) {
	t.Helper()
	if b.deleted {
		return
	}
	if b.id == "" {
		// A create that failed after the bucket existed left no ID.
		if b.id = tg.findBucket(key, b.app); b.id == "" {
			return
		}
	}
	if tg.keep && t.Failed() {
		t.Logf("keeping bucket %s of %s for debugging (GREGALE_E2E_KEEP=1)", b.id, b.app)
		return
	}
	for _, id := range b.credentials {
		tg.apiCall(http.MethodDelete, b.path()+"/s3-credentials/"+id, nil, key)
	}
	for range 10 {
		r := tg.apiCall(http.MethodGet, b.path()+"/objects?limit=1000", nil, key)
		docs, err := decodeObjects(r.body)
		if r.status != http.StatusOK || err != nil || len(docs) != 1 {
			break
		}
		items, _ := field(docs[0], "items").([]any)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			tg.apiCall(http.MethodDelete, b.path()+"/objects?"+url.Values{"key": {str(item, "key")}}.Encode(), nil, key)
		}
	}
	res := tg.run(t, 2*time.Minute, "bucket", "delete", b.app, b.id, "--yes")
	if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
		t.Logf("warning: cleanup could not delete bucket %s of %s (%s); its app cannot be deleted until it is", b.id, b.app, res.problem())
	}
}

// findBucket returns the ID of app's bucket named objectBucket, or "".
func (tg *target) findBucket(key, app string) string {
	r := tg.apiCall(http.MethodGet, "/v1/apps/"+app+"/buckets", nil, key)
	docs, err := decodeObjects(r.body)
	if r.status != http.StatusOK || err != nil || len(docs) != 1 {
		return ""
	}
	if ids := bucketIDs(docs[0]); len(ids) == 1 {
		return ids[0]
	}
	return ""
}
