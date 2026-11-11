// adr: 1027
package dbarchive

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
)

type multipartTransport struct {
	t                 *testing.T
	sizes             []int64
	complete, aborted bool
	fail              string
	cancel            context.CancelFunc
	readParts         bool
	bodies            []string
	attempts          int
}

func (m *multipartTransport) Do(req *http.Request) (*http.Response, error) {
	q := req.URL.Query()
	status := 200
	body := ""
	headers := http.Header{}
	switch {
	case req.Method == "POST" && q.Has("uploads"):
		body = `<InitiateMultipartUploadResult><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`
		if m.fail == "create" {
			status = 403
		}
	case req.Method == "PUT" && q.Get("uploadId") != "":
		m.attempts++
		length := req.ContentLength
		if decoded := req.Header.Get("X-Amz-Decoded-Content-Length"); decoded != "" {
			var err error
			length, err = strconv.ParseInt(decoded, 10, 64)
			if err != nil {
				m.t.Fatal(err)
			}
		}
		m.sizes = append(m.sizes, length)
		headers.Set("ETag", fmt.Sprintf(`"part-%s"`, q.Get("partNumber")))
		headers.Set("X-Amz-Checksum-Crc32", "AAAAAA==")
		if m.readParts {
			var reader io.Reader = req.Body
			if strings.Contains(req.Header.Get("Content-Encoding"), "aws-chunked") {
				reader = httputil.NewChunkedReader(reader)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return nil, err
			}
			m.bodies = append(m.bodies, string(data))
			var checksum [4]byte
			binary.BigEndian.PutUint32(checksum[:], crc32.ChecksumIEEE(data))
			headers.Set("X-Amz-Checksum-Crc32", base64.StdEncoding.EncodeToString(checksum[:]))
		}
		if m.cancel != nil {
			m.cancel()
			return nil, req.Context().Err()
		}
		if m.fail == "part" {
			status = 403
		}
		if m.fail == "retry" && m.attempts == 1 {
			status = 500
		}
		if m.fail == "etag" {
			headers.Del("ETag")
		}
	case req.Method == "POST" && q.Get("uploadId") != "":
		var got struct {
			Parts []struct {
				Number int    `xml:"PartNumber"`
				ETag   string `xml:"ETag"`
				CRC    string `xml:"ChecksumCRC32"`
			} `xml:"Part"`
		}
		if err := xml.NewDecoder(req.Body).Decode(&got); err != nil {
			m.t.Fatal(err)
		}
		expected := len(m.sizes)
		if m.fail == "retry" {
			expected--
		}
		if len(got.Parts) != expected {
			m.t.Fatalf("completed %d parts after %d uploads", len(got.Parts), len(m.sizes))
		}
		for i, p := range got.Parts {
			if p.Number != i+1 || p.ETag == "" || p.CRC == "" {
				m.t.Fatalf("invalid completed part: %+v", p)
			}
		}
		m.complete = true
		body = `<CompleteMultipartUploadResult><ETag>complete</ETag></CompleteMultipartUploadResult>`
		if m.fail == "complete" {
			status = 403
		}
	case req.Method == "DELETE" && q.Get("uploadId") != "":
		m.aborted = true
		if req.Context().Err() != nil {
			m.t.Fatal("abort inherited cancelled upload context")
		}
		if _, ok := req.Context().Deadline(); !ok {
			m.t.Fatal("abort has no cleanup deadline")
		}
	case req.Method == "PUT":
		m.sizes = append(m.sizes, req.ContentLength)
		headers.Set("ETag", "small-object")
	default:
		m.t.Fatalf("unexpected S3 request: %s %s", req.Method, req.URL)
	}
	if status >= 400 {
		body = `<Error><Code>AccessDenied</Code><Message>fixture upload failure</Message></Error>`
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
func multipartRemote(m *multipartTransport) *S3Remote {
	options := s3.Options{Region: "us-east-1", BaseEndpoint: aws.String("https://s3.test.invalid"), Credentials: aws.AnonymousCredentials{}, HTTPClient: m, UsePathStyle: true, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, Retryer: retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = 2
		o.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
	})}
	if !m.readParts {
		// Logical-size tests deliberately do not materialize terabytes of
		// data. Disable request checksum computation only for that fixture;
		// byte and retry tests below use the production checksum path.
		options.APIOptions = []func(*middleware.Stack) error{func(stack *middleware.Stack) error {
			return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("LogicalMultipartPayload", func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
				if input, ok := in.Parameters.(*s3.UploadPartInput); ok {
					input.ChecksumAlgorithm = ""
				}
				return next.HandleInitialize(ctx, in)
			}), middleware.Before)
		}}
	}
	return &S3Remote{bucket: "archive", client: s3.New(options)}
}

// sizedBackup represents large archives without allocating or reading gigabytes.
// The transport validates part boundaries without consuming their payloads.
type sizedBackup struct{ size, pos int64 }

func (b *sizedBackup) Read([]byte) (int, error) {
	return 0, errors.New("large fixture payload must be streamed only by a real transport")
}
func (b *sizedBackup) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		b.pos = offset
	case io.SeekCurrent:
		b.pos += offset
	case io.SeekEnd:
		b.pos = b.size + offset
	default:
		return 0, errors.New("invalid seek")
	}
	return b.pos, nil
}

func TestS3MultipartUploadBoundaries(t *testing.T) {
	for _, size := range []int64{1 << 20, s3PartSize, s3PartSize + 1, (5 << 30) + 1, 2 << 40} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			transport := &multipartTransport{t: t}
			remote := multipartRemote(transport)
			if err := remote.Put(t.Context(), "backup.tar.zst", &sizedBackup{size: size}, size); err != nil {
				t.Fatal(err)
			}
			var total int64
			for i, part := range transport.sizes {
				total += part
				if size > s3PartSize && (part > s3MaxPartSize || (i < len(transport.sizes)-1 && part < 5<<20)) {
					t.Fatalf("invalid part size %d", part)
				}
			}
			if total != size || len(transport.sizes) > int(s3MaxParts) || transport.aborted || transport.complete != (size > s3PartSize) {
				t.Fatalf("upload size=%d total=%d parts=%d complete=%t aborted=%t", size, total, len(transport.sizes), transport.complete, transport.aborted)
			}
		})
	}
}
func TestS3MultipartFailureAbortsUpload(t *testing.T) {
	for _, failure := range []string{"create", "part", "etag", "complete", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transport := &multipartTransport{t: t, fail: failure}
			if failure == "cancel" {
				transport.cancel = cancel
			}
			err := multipartRemote(transport).Put(ctx, "backup.tar.zst", &sizedBackup{size: s3PartSize + 1}, s3PartSize+1)
			if err == nil || transport.aborted != (failure != "create") {
				t.Fatalf("failure=%s err=%v aborted=%t", failure, err, transport.aborted)
			}
			if failure == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation cause: %v", err)
			}
		})
	}
}
func TestS3MultipartPartRetryRewindsOnlyItsSection(t *testing.T) {
	transport := &multipartTransport{t: t, readParts: true, fail: "retry"}
	source := strings.NewReader("prefix-payload")
	if _, err := source.Seek(7, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := multipartRemote(transport).putMultipart(t.Context(), "backup.tar.zst", source, 7); err != nil {
		t.Fatal(err)
	}
	// The completion has one part; the failed attempt must not count as one.
	if len(transport.bodies) != 2 || transport.bodies[0] != "payload" || transport.bodies[1] != "payload" {
		t.Fatalf("retried part bytes: %q", transport.bodies)
	}
}

func TestS3MultipartStreamsCompletePartBytes(t *testing.T) {
	transport := &multipartTransport{t: t, readParts: true}
	data := bytes.Repeat([]byte("x"), int(s3PartSize)+3)
	if err := multipartRemote(transport).Put(t.Context(), "backup.tar.zst", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if len(transport.bodies) != 2 || transport.bodies[0] != string(data[:s3PartSize]) || transport.bodies[1] != "xxx" {
		t.Fatal("multipart payloads did not reconstruct the backup")
	}
}
func TestS3MultipartRejectsImpossibleObjectBeforeCreatingUpload(t *testing.T) {
	transport := &multipartTransport{t: t}
	size := s3MaxParts*s3MaxPartSize + 1
	if err := multipartRemote(transport).Put(t.Context(), "backup.tar.zst", &sizedBackup{size: size}, size); err == nil || len(transport.sizes) != 0 || transport.complete || transport.aborted {
		t.Fatalf("oversized upload was started: %v", err)
	}
}
