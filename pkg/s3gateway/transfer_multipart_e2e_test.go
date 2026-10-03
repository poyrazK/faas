package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 411
func TestProductionMultipartTransferMem(t *testing.T) {
	productionMultipartTransfer(t, state.NewMemStore())
}
func TestProductionMultipartTransferPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	productionMultipartTransfer(t, st)
}

func productionMultipartTransfer(t *testing.T, st multipartCopyIntegrationStore) {
	const size = int64(65 << 20)
	p := &transferHTTPFixture{path: filepath.Join(t.TempDir(), "part")}
	var completed atomic.Bool
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodGet && q.Has("uploads"):
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
		case r.Method == http.MethodPost && q.Has("uploads"):
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>private-transfer</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodGet && q.Get("uploadId") == "private-transfer":
			_, _ = fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>&quot;local-transfer&quot;</ETag><Size>%d</Size></Part></ListPartsResult>`, size)
		case r.Method == http.MethodPost && q.Get("uploadId") == "private-transfer":
			_, _ = io.Copy(io.Discard, r.Body)
			completed.Store(true)
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;local-transfer&quot;</ETag></CompleteMultipartUploadResult>`)
		case r.Method == http.MethodHead && !completed.Load():
			w.WriteHeader(http.StatusNotFound)
		default:
			p.serve(t, w, r)
		}
	})
	f := newMultipartCopyIntegrationWithTransfer(t, st, origin, transferIntegrationConfig(3600), 0)
	id := f.initiate(t, "large-part")
	source, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = source.Truncate(size); err != nil {
		t.Fatal(err)
	}
	out, err := f.client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String("large-part"), UploadId: aws.String(id), PartNumber: aws.Int32(1), Body: io.NewSectionReader(source, 0, size), ContentLength: aws.Int64(size)})
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(f.handler.spoolDir)
	if err != nil || len(files) != 0 || f.handler.spoolReserved != 0 || len(f.handler.putSlots) != 0 {
		t.Fatal("multipart part buffered or leaked resources", files, err)
	}
	listed, err := f.client.ListParts(t.Context(), &awss3.ListPartsInput{Bucket: aws.String("assets"), Key: aws.String("large-part"), UploadId: aws.String(id)})
	if err != nil || len(listed.Parts) != 1 || aws.ToInt64(listed.Parts[0].Size) != size {
		t.Fatal("large part not listed", listed, err)
	}
	_, err = f.client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("large-part"), UploadId: aws.String(id), MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: out.ETag}}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String("large-part")})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, read.Body)
	closeErr := read.Body.Close()
	p.mu.Lock()
	digest := p.digest
	p.mu.Unlock()
	if err != nil || closeErr != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != digest || p.puts.Load() != 1 {
		t.Fatal("multipart bytes changed or replayed", n, err, closeErr)
	}
	expected := sha256.New()
	if _, err = io.Copy(expected, io.NewSectionReader(source, 0, size)); err != nil || hex.EncodeToString(expected.Sum(nil)) != digest {
		t.Fatal("multipart source bytes changed", err)
	}
	u, err := st.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, id)
	if err != nil || u.State != state.ObjectMultipartCompleted || u.SizeBytes != size || u.CompletionETag != aws.ToString(out.ETag) {
		t.Fatal("multipart result not persisted", u, err)
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now()).CapacityBytes != size {
		t.Fatal("large multipart accounting", usage, err)
	}
}
