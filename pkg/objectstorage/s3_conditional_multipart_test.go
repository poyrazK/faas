package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestS3ConditionalMultipartProtocolAndRecovery(t *testing.T) {
	for _, test := range []struct {
		name, code, session string
		status, headStatus  int
		size                int64
		condition           ObjectWriteConditions
		want                error
	}{
		{"create", "", "", 200, 0, 0, ObjectWriteConditions{IfNoneMatch: "*"}, nil},
		{"replace", "", "", 200, 0, 0, ObjectWriteConditions{IfMatch: `"old"`}, nil},
		{"existing", "PreconditionFailed", "other", 412, 200, 10, ObjectWriteConditions{IfNoneMatch: "*"}, ErrPreconditionFailed},
		{"race", "ConditionalRequestConflict", "other", 409, 200, 10, ObjectWriteConditions{IfMatch: `"old"`}, ErrConditionalConflict},
		{"embedded-error", "ConditionalRequestConflict", "other", 200, 200, 10, ObjectWriteConditions{IfNoneMatch: "*"}, ErrConditionalConflict},
		{"missing-destination", "NoSuchKey", "", 404, 404, 0, ObjectWriteConditions{IfMatch: `"old"`}, ErrConditionalNotFound},
		{"lost-response", "NoSuchUpload", "session", 404, 200, 10, ObjectWriteConditions{IfNoneMatch: "*"}, nil},
		{"lost-response-condition", "PreconditionFailed", "session", 412, 200, 10, ObjectWriteConditions{IfMatch: `"old"`}, nil},
		{"lost-response-race", "ConditionalRequestConflict", "session", 409, 200, 10, ObjectWriteConditions{IfNoneMatch: "*"}, nil},
		{"wrong-size", "NoSuchUpload", "session", 404, 200, 11, ObjectWriteConditions{IfNoneMatch: "*"}, ErrConditionalConflict},
		{"wrong-session", "NoSuchUpload", "other", 404, 200, 10, ObjectWriteConditions{IfNoneMatch: "*"}, ErrConditionalConflict},
		{"missing-upload-and-object", "NoSuchUpload", "", 404, 404, 0, ObjectWriteConditions{IfNoneMatch: "*"}, ErrConditionalConflict},
		{"unavailable-proof", "PreconditionFailed", "", 412, 403, 0, ObjectWriteConditions{IfNoneMatch: "*"}, ErrUnavailable},
		{"transient", "SlowDown", "", 503, 0, 0, ObjectWriteConditions{IfNoneMatch: "*"}, ErrUnavailable},
		{"missing-bucket", "NoSuchBucket", "", 404, 0, 0, ObjectWriteConditions{IfNoneMatch: "*"}, ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls, heads int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				if r.Method == http.MethodHead {
					heads++
					w.Header().Set("Content-Length", strconv.FormatInt(test.size, 10))
					w.Header().Set("X-Amz-Meta-Gregale-Upload-Id", test.session)
					w.WriteHeader(test.headStatus)
					return
				}
				calls++
				if r.Method != http.MethodPost || r.URL.Query().Get("uploadId") != "provider" || r.Header.Get("If-Match") != test.condition.IfMatch || r.Header.Get("If-None-Match") != test.condition.IfNoneMatch {
					t.Errorf("lost conditional request: %s %s %v", r.Method, r.URL, r.Header)
				}
				w.WriteHeader(test.status)
				if test.code == "" {
					_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
				} else {
					_, _ = fmt.Fprintf(w, `<Error><Code>%s</Code><Message>provider-secret</Message></Error>`, test.code)
				}
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint = upstream.URL
			provider, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			err = CompleteMultipart(context.Background(), provider, "gregale-test", MultipartCompleteRequest{SessionID: "session", Key: "key", ProviderUploadID: "provider", SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}, test.condition)
			if !errors.Is(err, test.want) || calls != 1 || test.headStatus == 0 && heads != 0 || test.headStatus != 0 && heads != 1 {
				t.Fatalf("err=%v want=%v calls=%d heads=%d", err, test.want, calls, heads)
			}
		})
	}
}

func TestConditionalMultipartCapabilityAndValidation(t *testing.T) {
	config := testBackend()
	provider, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	// Embedding just Provider deliberately hides its optional capabilities.
	unsupported := struct{ Provider }{provider}
	request := MultipartCompleteRequest{SessionID: "session", Key: "key", ProviderUploadID: "provider", SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}
	if err = CompleteMultipart(context.Background(), unsupported, "bucket", request, ObjectWriteConditions{IfNoneMatch: "*"}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	for _, condition := range []ObjectWriteConditions{{IfNoneMatch: "etag"}, {IfMatch: "a", IfNoneMatch: "*"}, {IfMatch: "a\nb"}} {
		if err = CompleteMultipart(context.Background(), provider, "bucket", request, condition); !errors.Is(err, ErrInvalid) {
			t.Fatal(condition, err)
		}
	}
}
