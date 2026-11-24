package storagegrant_test

// adr: 20261117190000

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	gcs "cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/option"

	"github.com/onebox-faas/faas/pkg/storagegrant"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

const (
	dep     = "0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f"
	acct    = "11111111-2222-4333-8444-555555555555"
	job     = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	other   = "99999999-8888-4777-8666-555555555555"
	capture = "c0ffee00-0000-4000-8000-000000000001"
)

func TestResolve(t *testing.T) {
	platform := storagegrant.Subject{Class: storagegrant.ClassPlatform}
	deployment := storagegrant.Subject{Class: storagegrant.ClassDeployment, ID: dep}
	sandbox := storagegrant.Subject{Class: storagegrant.ClassSandboxAccount, ID: acct}
	jobSubject := storagegrant.Subject{Class: storagegrant.ClassJob, ID: job}
	unknown := storagegrant.Subject{Class: storagegrant.ClassUnknown}
	for key, want := range map[string]storagegrant.Subject{
		// Every key builder in storagekey resolves.
		storagekey.BaseForArch("node22", "amd64"):                            platform,
		storagekey.BaseGeneration("base/base-amd64.ext4", "g1"):              platform,
		storagekey.Kernel("1.12.0"):                                          platform,
		storagekey.AgentRunLayerForArch("amd64"):                             platform,
		storagekey.SandboxLayerForArch("arm64"):                              platform,
		storagekey.AppLayer("shop", dep):                                     deployment,
		storagekey.AppSidecarLayer("shop", dep, "redis"):                     deployment,
		storagekey.Layer(dep):                                                deployment,
		storagekey.SnapshotMem(dep):                                          deployment,
		storagekey.SnapshotVMState(dep):                                      deployment,
		storagekey.SnapshotWarmMem(dep):                                      deployment,
		storagekey.SnapshotWarmVMState(dep):                                  deployment,
		storagekey.SnapshotMem(dep) + ".age":                                 deployment,
		storagekey.JobLayer(job):                                             jobSubject,
		storagekey.JobLayerAttempt(job, other):                               jobSubject,
		storagekey.SandboxScreenshot(acct, other, job):                       sandbox,
		"sources/" + job + ".tar.gz":                                         {Class: storagegrant.ClassBuild, ID: job},
		"sources/" + job + ".tar":                                            unknown,
		"snap/" + dep + "/captures/" + capture + "/v2/mem":                   deployment,
		"snap/" + dep + "/captures/" + capture + "/v2/mem.age":               deployment,
		"snap/" + dep + "/warm/captures/" + capture + "/v2/drive":            deployment,
		"snap/sandbox-template-" + acct + "/captures/" + capture + "/v2/mem": sandbox,
		"snap/sandbox-preset/captures/" + capture + "/v2/mem":                platform,
		// Sidecars belong to what they describe.
		"sigs/" + storagekey.AppLayer("shop", dep) + ".sig":          deployment,
		"sigs/" + storagekey.BaseForArch("node22", "amd64") + ".sig": platform,
		"sigs/" + storagekey.JobLayer(job) + ".sig":                  jobSubject,
		"sigs/sigs/" + storagekey.Layer(dep) + ".sig.sig":            unknown,
		"sigs/" + storagekey.Layer(dep):                              unknown,
		"scans/runner-node22-amd64.ext4.scan.json":                   platform,
		"scans/base-amd64.ext4.scan.json":                            platform,
		"scans/x/y.scan.json":                                        unknown,
		"scans/.scan.json":                                           unknown,
		"scans/generations/ef49df9f2058b9f3ffddcf2ad302a0e774cb61eed79b145766a276903a4e2038/base-amd64.ext4.scan.json":   platform,
		"scans/generations/ef49df9f2058b9f3ffddcf2ad302a0e774cb61eed79b145766a276903a4e2038/x/base-amd64.ext4.scan.json": unknown,
		"scans/generations/ABC/base-amd64.ext4.scan.json":                                                                unknown,
		"scans/generations/ef49df9f2058b9f3ffddcf2ad302a0e774cb61eed79b145766a276903a4e2038/...scan.json":                unknown,
		"base/generations/ef49df9f2058b9f3ffddcf2ad302a0e774cb61eed79b145766a276903a4e2038/base-amd64.ext4":              platform,
		"sboms/" + job + ".cdx.json": {Class: storagegrant.ClassBuildOutput, ID: job},
		"sboms/nope.cdx.json":        unknown,
		// Refused: unknown prefixes, traversal, non-canonical IDs, odd shapes.
		"sbom/whatever.json":            unknown,
		"base":                          unknown,
		"base/":                         unknown,
		"/base/x":                       unknown,
		"apps/shop/../" + dep + ".ext4": unknown,
		"apps/shop//" + dep + ".ext4":   unknown,
		"apps/shop/" + strings.ToUpper(dep) + ".ext4":                unknown,
		"apps/shop/" + dep + ".tar":                                  unknown,
		"apps/shop/" + dep + "-.ext4":                                unknown,
		"apps/shop/x/" + dep + ".ext4":                               unknown,
		"layers/not-a-uuid.ext4":                                     unknown,
		"jobs/" + job:                                                unknown,
		"jobs/" + job + "__x.ext4":                                   unknown,
		"snap/" + dep:                                                unknown,
		"snap/" + dep + "/captures/" + capture + "/v1/mem":           unknown,
		"snap/sandbox-template-nope/captures/" + capture + "/v2/mem": unknown,
		"snap/someone/mem":                                           unknown,
		"sandbox-screenshots/" + acct + "/" + other + "/x.jpg":       unknown,
		"base/x?generation=1":                                        unknown,
	} {
		if got := storagegrant.Resolve(key); got != want {
			t.Errorf("Resolve(%q) = %+v, want %+v", key, got, want)
		}
	}
}

func TestRequestValidate(t *testing.T) {
	key := storagekey.Layer(dep)
	for name, tc := range map[string]struct {
		r  storagegrant.Request
		ok bool
	}{
		"get":                  {storagegrant.Request{Key: key, Op: storagegrant.OpGet}, true},
		"get generation":       {storagegrant.Request{Key: key, Op: storagegrant.OpGetGeneration, Generation: 3}, true},
		"create with metadata": {storagegrant.Request{Key: key, Op: storagegrant.OpCreate, Metadata: map[string]string{"compression": "zstd"}}, true},
		"replace":              {storagegrant.Request{Key: key, Op: storagegrant.OpReplace, Generation: 9}, true},
		"get with generation":  {storagegrant.Request{Key: key, Op: storagegrant.OpGet, Generation: 3}, false},
		"replace without":      {storagegrant.Request{Key: key, Op: storagegrant.OpReplace}, false},
		"read with metadata":   {storagegrant.Request{Key: key, Op: storagegrant.OpGet, Metadata: map[string]string{"a": "b"}}, false},
		"header injection":     {storagegrant.Request{Key: key, Op: storagegrant.OpCreate, Metadata: map[string]string{"a": "b\r\nx-evil: 1"}}, false},
		"bad metadata name":    {storagegrant.Request{Key: key, Op: storagegrant.OpCreate, Metadata: map[string]string{"a b": "c"}}, false},
		"head":                 {storagegrant.Request{Key: key, Op: storagegrant.OpHead}, true},
		"s3 replace by etag":   {storagegrant.Request{Key: key, Op: storagegrant.OpReplace, ETag: `"abc"`}, true},
		"replace with both":    {storagegrant.Request{Key: key, Op: storagegrant.OpReplace, Generation: 2, ETag: `"abc"`}, false},
		"create multipart":     {storagegrant.Request{Key: key, Op: storagegrant.OpCreateMultipart, Metadata: map[string]string{"a": "b"}}, true},
		"upload part":          {storagegrant.Request{Key: key, Op: storagegrant.OpUploadPart, UploadID: "u1", PartNumber: 3}, true},
		"part without upload":  {storagegrant.Request{Key: key, Op: storagegrant.OpUploadPart, PartNumber: 3}, false},
		"part 0":               {storagegrant.Request{Key: key, Op: storagegrant.OpUploadPart, UploadID: "u1"}, false},
		"part with metadata":   {storagegrant.Request{Key: key, Op: storagegrant.OpUploadPart, UploadID: "u1", PartNumber: 1, Metadata: map[string]string{"a": "b"}}, false},
		"complete create-only": {storagegrant.Request{Key: key, Op: storagegrant.OpCompleteMultipart, UploadID: "u1"}, true},
		"complete replace":     {storagegrant.Request{Key: key, Op: storagegrant.OpCompleteMultipart, UploadID: "u1", ETag: `"abc"`}, true},
		"abort":                {storagegrant.Request{Key: key, Op: storagegrant.OpAbortMultipart, UploadID: "u1"}, true},
		"upload id injection":  {storagegrant.Request{Key: key, Op: storagegrant.OpAbortMultipart, UploadID: "u1\r\nx: y"}, false},
		"unknown op":           {storagegrant.Request{Key: key, Op: "delete"}, false},
		"unknown key":          {storagegrant.Request{Key: "sbom/x", Op: storagegrant.OpGet}, false},
	} {
		if err := tc.r.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate = %v, want ok=%v", name, err, tc.ok)
		}
	}
}

func testKeyPEM(t *testing.T) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func TestGCSSigner(t *testing.T) {
	client, err := gcs.NewClient(context.Background(), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	s, err := storagegrant.NewGCSKeySigner(client, "artifacts", "cp@example.iam.gserviceaccount.com", testKeyPEM(t))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(storagegrant.TTL)
	key := "snap/" + dep + "/captures/" + capture + "/v2/mem"

	get, err := s.Sign(t.Context(), storagegrant.Request{Key: key, Op: storagegrant.OpGetGeneration, Generation: 42}, expires)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(get.URL)
	if get.Method != "GET" || !strings.HasSuffix(u.Path, "/artifacts/"+key) || u.Query().Get("generation") != "42" ||
		u.Query().Get("X-Goog-Algorithm") != "GOOG4-RSA-SHA256" || len(get.Headers) != 0 {
		t.Fatalf("generation read grant = %+v", get)
	}

	create, err := s.Sign(t.Context(), storagegrant.Request{Key: key, Op: storagegrant.OpCreate,
		ContentType: "application/octet-stream", Metadata: map[string]string{"Compression": "zstd"}}, expires)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(create.URL)
	signed := u.Query().Get("X-Goog-SignedHeaders")
	if create.Method != "PUT" || create.Headers["x-goog-if-generation-match"] != "0" || create.Headers["x-goog-meta-compression"] != "zstd" ||
		create.Headers["content-type"] != "application/octet-stream" ||
		!strings.Contains(signed, "x-goog-if-generation-match") || !strings.Contains(signed, "x-goog-meta-compression") || !strings.Contains(signed, "content-type") {
		t.Fatalf("create grant = %+v signed=%q; want the condition, metadata and content type signed", create, signed)
	}

	replace, err := s.Sign(t.Context(), storagegrant.Request{Key: key, Op: storagegrant.OpReplace, Generation: 7}, expires)
	if err != nil || replace.Headers["x-goog-if-generation-match"] != "7" {
		t.Fatalf("replace grant = %+v, %v", replace, err)
	}
	if _, err := s.Sign(t.Context(), storagegrant.Request{Key: "sbom/x", Op: storagegrant.OpGet}, expires); err == nil {
		t.Fatal("signed a key outside the keyspace")
	}
}

func TestS3Signer(t *testing.T) {
	client := s3.New(s3.Options{Region: "us-east-1", Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "secret", "")),
		BaseEndpoint: aws.String("https://s3.example.test"), UsePathStyle: true})
	s, err := storagegrant.NewS3Signer(client, "artifacts")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(storagegrant.TTL)
	key := storagekey.AppLayer("shop", dep)

	get, err := s.Sign(t.Context(), storagegrant.Request{Key: key, Op: storagegrant.OpGet}, expires)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(get.URL)
	if get.Method != "GET" || u.Path != "/artifacts/"+key || u.Query().Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || u.Query().Get("X-Amz-Expires") == "" {
		t.Fatalf("get grant = %+v", get)
	}
	create, err := s.Sign(t.Context(), storagegrant.Request{Key: key, Op: storagegrant.OpCreate, Metadata: map[string]string{"compression": "zstd"}}, expires)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(create.URL)
	if create.Method != "PUT" || create.Headers["if-none-match"] != "*" || !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), "if-none-match") {
		t.Fatalf("create grant = %+v; want If-None-Match: * signed", create)
	}
	for _, op := range []storagegrant.Request{
		{Key: key, Op: storagegrant.OpGetGeneration, Generation: 1},
		{Key: key, Op: storagegrant.OpReplace, Generation: 1},
	} {
		if _, err := s.Sign(t.Context(), op, expires); !errors.Is(err, storagegrant.ErrUnsupported) {
			t.Errorf("%s on S3: err = %v, want ErrUnsupported", op.Op, err)
		}
	}
}
