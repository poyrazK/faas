package objectstorage

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEncryptionPublicMetadata(t *testing.T) {
	_, b := encryptionTestRegistry(t, encryptionTestBackend())
	e := encryptionSelection(t, b, "aws:kms")
	caps := b.Encryption.PublicCapabilities(encryptionTestAccount)
	if len(caps.KeyIDs) != 1 || caps.KeyIDs[0] != e.Selection.KeyID || len(b.Encryption.PublicCapabilities(uuid.NewString()).KeyIDs) != 0 {
		t.Fatal("foreign discovery", caps)
	}
	caps.Algorithms[0] = "changed"
	if b.Encryption.Algorithms[0] == "changed" {
		t.Fatal("discovery aliases enrollment")
	}
	for _, tc := range []struct {
		name   string
		mutate func(http.Header)
		bad    bool
	}{
		{name: "owned"},
		{name: "duplicate mode", mutate: func(h http.Header) { h.Add("X-Amz-Server-Side-Encryption", "aws:kms") }, bad: true},
		{name: "case duplicate key", mutate: func(h http.Header) { h["x-amz-server-side-encryption-aws-kms-key-id"] = []string{e.ProviderKeyID} }, bad: true},
		{name: "unknown key", mutate: func(h http.Header) { h.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", "private-unenrolled") }, bad: true},
		{name: "missing key", mutate: func(h http.Header) { h.Del("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") }, bad: true},
		{name: "bad bucket boolean", mutate: func(h http.Header) { h.Set("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled", "TRUE") }, bad: true},
		{name: "customer key", mutate: func(h http.Header) { h.Set("X-Amz-Server-Side-Encryption-Customer-Key", "private") }, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := encryptionFixtureHeaders(e)
			if tc.mutate != nil {
				tc.mutate(h)
			}
			out, err := b.Encryption.PublicReadEncryption(encryptionTestAccount, h)
			if (err != nil) != tc.bad {
				t.Fatal(out, err)
			}
			if !tc.bad && out.KeyID != e.Selection.KeyID {
				t.Fatal(out)
			}
			_, err = VerifyEncryptionAcknowledgment(h, e)
			if (err != nil) != tc.bad {
				t.Fatal("acknowledgment", err)
			}
		})
	}
	if _, err := b.Encryption.PublicReadEncryption(uuid.NewString(), encryptionFixtureHeaders(e)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("foreign read mapping", err)
	}
	if out, err := b.Encryption.PublicReadEncryption(encryptionTestAccount, http.Header{}); err != nil || !out.Empty() {
		t.Fatal(out, err)
	}
}

// adr: 414
func TestEncryptedMultipartAdoptionWithDisabledKey(t *testing.T) {
	var lists, creates int
	p, b := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("uploads") {
			lists++
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>existing</Key><UploadId>private-existing</UploadId></Upload></ListMultipartUploadsResult>`)
			return
		}
		creates++
		t.Error("disabled key created a new native upload")
		w.WriteHeader(500)
	}, strings.Replace(strings.Replace(encryptionKeyResponse(), `"Enabled":true`, `"Enabled":false`, 1), `"KeyState":"Enabled"`, `"KeyState":"Disabled"`, 1))
	e := encryptionSelection(t, b, "aws:kms")
	r := MultipartCreateRequest{SessionID: uuid.NewString(), Key: "existing"}
	id, err := p.EnsureEncryptedMultipart(t.Context(), "physical", r, e)
	if err != nil || id != "private-existing" {
		t.Fatal("disabled-key adoption", id, err)
	}
	r.Key = "new"
	if _, err = p.EnsureEncryptedMultipart(t.Context(), "physical", r, e); !errors.Is(err, ErrConfiguration) {
		t.Fatal("new native upload with disabled key", err)
	}
	if lists != 2 || creates != 0 {
		t.Fatal(lists, creates)
	}
}

func TestEncryptedCopyDispatchRecorderFailure(t *testing.T) {
	var writes int
	p, b := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) { writes++; w.WriteHeader(500) }, encryptionKeyResponse())
	e := encryptionSelection(t, b, "aws:kms")
	blocked := errors.New("dispatch fence unavailable")
	ctx := WithEncryptionWriteRecorder(t.Context(), func(context.Context) error { return blocked })
	_, err := p.CopyEncryptedObject(ctx, "physical", uuid.NewString(), CopyObjectRequest{SourceKey: "source", DestinationKey: "dest", MetadataDirective: "REPLACE", TaggingDirective: "REPLACE"}, CopySourceSnapshot{SizeBytes: 1, ETag: `"source"`}, CopySourceConditions{}, e)
	if !errors.Is(err, blocked) || !errors.Is(err, ErrWriteRejected) || writes != 0 {
		t.Fatal("write escaped failed dispatch", err, writes)
	}
}
