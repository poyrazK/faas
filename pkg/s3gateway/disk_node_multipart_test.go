package s3gateway

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func diskMultipartServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	var mu sync.Mutex
	var store *objectnode.Store
	var handler *DiskHandler
	restart := func() {
		mu.Lock()
		defer mu.Unlock()
		if store != nil {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		}
		var err error
		store, err = objectnode.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		handler, err = NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1", MinFreeBytes: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	restart()
	t.Cleanup(func() {
		if store != nil {
			store.Close()
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); defer mu.Unlock(); handler.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	return server, restart
}

func TestDiskNodeMultipartSDKResume(t *testing.T) {
	server, restart := diskMultipartServer(t)
	client := diskClient(server)
	bucket, key := aws.String("test-bucket"), aws.String("folder/large +ç.txt")
	if _, err := client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: bucket}); err != nil {
		t.Fatal(err)
	}
	created, err := client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: bucket, Key: key, ContentType: aws.String("text/plain"), Metadata: map[string]string{"owner": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("x"), int(api.MinMultipartPartBytes))
	first, err := client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: bucket, Key: key, UploadId: created.UploadId, PartNumber: aws.Int32(1), Body: bytes.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	restart()
	list, err := client.ListParts(t.Context(), &awss3.ListPartsInput{Bucket: bucket, Key: key, UploadId: created.UploadId, MaxParts: aws.Int32(1)})
	if err != nil || len(list.Parts) != 1 || aws.ToString(list.Parts[0].ETag) != aws.ToString(first.ETag) {
		t.Fatal(list, err)
	}
	active, err := client.ListMultipartUploads(t.Context(), &awss3.ListMultipartUploadsInput{Bucket: bucket, Prefix: aws.String("folder/"), EncodingType: types.EncodingTypeUrl})
	if err != nil || len(active.Uploads) != 1 || aws.ToString(active.Uploads[0].UploadId) != aws.ToString(created.UploadId) {
		t.Fatal(active, err)
	}
	last, err := client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: bucket, Key: key, UploadId: created.UploadId, PartNumber: aws.Int32(2), Body: strings.NewReader("tail")})
	if err != nil {
		t.Fatal(err)
	}
	request := &awss3.CompleteMultipartUploadInput{Bucket: bucket, Key: key, UploadId: created.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: first.ETag}, {PartNumber: aws.Int32(2), ETag: last.ETag}}}}
	completed, err := client.CompleteMultipartUpload(t.Context(), request)
	if err != nil || !strings.HasSuffix(aws.ToString(completed.ETag), `-2"`) {
		t.Fatal(completed, err)
	}
	restart()
	get, err := client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: bucket, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(get.Body)
	get.Body.Close()
	if err != nil || !bytes.Equal(actual, append(body, []byte("tail")...)) || get.Metadata["owner"] != "test" {
		t.Fatal("resumed bytes/metadata changed", err)
	}
	if _, err = client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: bucket, Key: key}); err != nil {
		t.Fatal(err)
	}
	replayed, err := client.CompleteMultipartUpload(t.Context(), request)
	if err != nil || aws.ToString(replayed.ETag) != aws.ToString(completed.ETag) {
		t.Fatal(replayed, err)
	}
	_, err = client.HeadObject(t.Context(), &awss3.HeadObjectInput{Bucket: bucket, Key: key})
	assertSDKErrorCode(t, err, "NotFound")
	provider, err := objectstorage.NewS3(objectstorage.BackendConfig{Endpoint: server.URL, S3Region: "us-east-1", PathStyle: true, OwnedNodeReceipts: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}, func(name string) string {
		if name == "" {
			return ""
		}
		return diskTestCredential
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := provider.(objectstorage.MultipartResultCompleter).CompleteMultipartWithResult(t.Context(), aws.ToString(bucket), objectstorage.MultipartCompleteRequest{SessionID: get.Metadata[objectstorage.ReservedMultipartSessionMetadataKey], ProviderUploadID: aws.ToString(created.UploadId), Key: aws.ToString(key), SizeBytes: int64(len(body)) + 4, Recovering: true, Parts: []objectstorage.CompletedPart{{PartNumber: 1, ETag: aws.ToString(first.ETag)}, {PartNumber: 2, ETag: aws.ToString(last.ETag)}}}, objectstorage.ObjectWriteConditions{})
	if err != nil || proof.ETag != aws.ToString(completed.ETag) {
		t.Fatal("private historical completion proof", proof, err)
	}
}

func TestDiskNodeMultipartChecksums(t *testing.T) {
	for _, policy := range []objectchecksum.Policy{{Algorithm: "CRC32", Type: objectchecksum.Composite}, {Algorithm: "CRC64NVME", Type: objectchecksum.FullObject}, {Algorithm: "SHA256", Type: objectchecksum.Composite}} {
		t.Run(policy.Algorithm, func(t *testing.T) {
			_, handler := diskFixture(t)
			server := httptest.NewServer(handler)
			defer server.Close()
			client := diskClient(server)
			bucket, key := aws.String("test-bucket"), aws.String("key")
			if _, err := client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: bucket}); err != nil {
				t.Fatal(err)
			}
			u, err := client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: bucket, Key: key, ChecksumAlgorithm: types.ChecksumAlgorithm(policy.Algorithm), ChecksumType: types.ChecksumType(policy.Type)})
			if err != nil {
				t.Fatal(err)
			}
			h := objectchecksum.NewHash(policy.Algorithm)
			h.Write([]byte("bytes"))
			value := base64.StdEncoding.EncodeToString(h.Sum(nil))
			part := &awss3.UploadPartInput{Bucket: bucket, Key: key, UploadId: u.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("bytes"), ChecksumAlgorithm: types.ChecksumAlgorithm(policy.Algorithm)}
			selected := types.CompletedPart{PartNumber: aws.Int32(1)}
			switch policy.Algorithm {
			case "CRC32":
				part.ChecksumCRC32 = aws.String(value)
				selected.ChecksumCRC32 = aws.String(value)
			case "CRC64NVME":
				part.ChecksumCRC64NVME = aws.String(value)
				selected.ChecksumCRC64NVME = aws.String(value)
			case "SHA256":
				part.ChecksumSHA256 = aws.String(value)
				selected.ChecksumSHA256 = aws.String(value)
			}
			ack, err := client.UploadPart(t.Context(), part)
			if err != nil {
				t.Fatal(err)
			}
			selected.ETag = ack.ETag
			list, err := client.ListParts(t.Context(), &awss3.ListPartsInput{Bucket: bucket, Key: key, UploadId: u.UploadId})
			if err != nil || len(list.Parts) != 1 || string(list.ChecksumAlgorithm) != policy.Algorithm || string(list.ChecksumType) != policy.Type {
				t.Fatal(list, err)
			}
			out, err := client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: bucket, Key: key, UploadId: u.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{selected}}})
			if err != nil || string(out.ChecksumType) != policy.Type {
				t.Fatal(out, err)
			}
			head, err := client.HeadObject(t.Context(), &awss3.HeadObjectInput{Bucket: bucket, Key: key, ChecksumMode: types.ChecksumModeEnabled})
			if err != nil || string(head.ChecksumType) != policy.Type {
				t.Fatal(head, err)
			}
		})
	}
}
