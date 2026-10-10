package objectstorage

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// adr: 955
// The caller provisions and removes the private, disposable GCS fixture.
func TestGCSLiveConditionalQualification(t *testing.T) {
	if os.Getenv("FAAS_GCS_CONDITIONAL_LIVE") != "1" {
		t.Skip("disposable live GCS fixture required")
	}
	bucket := os.Getenv("FAAS_GCS_FEATURE_BUCKET")
	if !strings.HasPrefix(bucket, "gregale-cli-") || os.Getenv("FAAS_OBJECT_STORAGE_CONFIG") == "" {
		t.Fatal("unsafe or absent native qualification fixture")
	}
	registry, err := Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default(registry.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := backend.Provider.(*GCS)
	if !ok {
		t.Fatal("qualification requires GCS")
	}
	key := "qualification/conditional-race/" + uuid.NewString()
	ctx := WithConditionalWriteRequestRecorder(t.Context(), func(context.Context) error { return nil })
	sign := func(t *testing.T, body string, conditions ObjectWriteConditions) SignedRequest {
		t.Helper()
		size := int64(len(body))
		out, err := p.PresignConditionalPut(ctx, bucket, SignRequest{Method: http.MethodPut, Key: key, SizeBytes: &size, ExpiresIn: 60}, conditions)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	send := func(signed SignedRequest, body string) (int, string, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, signed.URL, strings.NewReader(body))
		if err != nil {
			return 0, "", err
		}
		for name, value := range signed.Headers {
			request.Header.Set(name, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return 0, "", err
		}
		defer response.Body.Close()
		var rejection struct {
			Code string `xml:"Code"`
		}
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusForbidden {
			if err := xml.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&rejection); err != nil {
				return response.StatusCode, "", err
			}
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
		return response.StatusCode, rejection.Code, nil
	}
	compete := func(t *testing.T, body string, conditions ObjectWriteConditions) {
		t.Helper()
		requests := []SignedRequest{sign(t, body, conditions), sign(t, body, conditions)}
		type result struct {
			status int
			err    error
		}
		start, results := make(chan struct{}), make(chan result, 2)
		for _, request := range requests {
			go func() {
				<-start
				status, _, err := send(request, body)
				results <- result{status, err}
			}()
		}
		close(start)
		statuses := map[int]int{}
		for range requests {
			out := <-results
			if out.err != nil {
				t.Fatal("native conditional request interrupted")
			}
			statuses[out.status]++
		}
		if statuses[http.StatusOK] != 1 || statuses[http.StatusPreconditionFailed] != 1 {
			t.Fatal("native generation fence did not admit exactly one writer", statuses)
		}
	}
	if !t.Run("competing create-only writers", func(t *testing.T) {
		compete(t, "first body", ObjectWriteConditions{IfNoneMatch: "*"})
	}) {
		return
	}
	first, err := p.gcsXMLProofObject(ctx, bucket, key, "")
	if err != nil {
		t.Fatal(err)
	}
	if !t.Run("competing identical replacements", func(t *testing.T) {
		compete(t, "replacement body", ObjectWriteConditions{IfMatch: first.ETag})
	}) {
		return
	}
	t.Run("stale ETag rejected before signing", func(t *testing.T) {
		size := int64(3)
		out, err := p.PresignConditionalPut(ctx, bucket, SignRequest{Method: http.MethodPut, Key: key, SizeBytes: &size}, ObjectWriteConditions{IfMatch: first.ETag})
		if !errors.Is(err, ErrPreconditionFailed) || out.URL != "" {
			t.Fatal("stale native ETag was signed", err)
		}
	})
	t.Run("missing wildcard target rejected", func(t *testing.T) {
		size := int64(3)
		out, err := p.PresignConditionalPut(ctx, bucket, SignRequest{Method: http.MethodPut, Key: key + "/absent", SizeBytes: &size}, ObjectWriteConditions{IfMatch: "*"})
		if !errors.Is(err, ErrNotFound) || out.URL != "" {
			t.Fatal("absent native target was signed", err)
		}
	})
	t.Run("generation predicate cannot be removed or changed", func(t *testing.T) {
		before, err := p.store.ObjectState(ctx, bucket, key)
		if err != nil || before.Version <= 0 || before.ETag == "" {
			t.Fatal("native generation proof unavailable before tampering")
		}
		for _, mutation := range []string{"removed", "changed"} {
			signed := sign(t, "tampered", ObjectWriteConditions{IfMatch: "*"})
			mutated := false
			for name := range signed.Headers {
				if strings.EqualFold(name, "X-Goog-If-Generation-Match") {
					if mutation == "removed" {
						delete(signed.Headers, name)
					} else {
						signed.Headers[name] = "0"
					}
					mutated = true
				}
			}
			if !mutated {
				t.Fatal("native capability omitted its generation predicate")
			}
			status, code, err := send(signed, "tampered")
			// Live GCS reports a removed signed header as MalformedSecurityHeader/400;
			// its XML API also defines MissingSecurityHeader for a missing required header.
			// a changed signed value is SignatureDoesNotMatch/403.
			rejected := status == http.StatusForbidden && code == "SignatureDoesNotMatch"
			if mutation == "removed" {
				rejected = rejected || status == http.StatusBadRequest && (code == "MalformedSecurityHeader" || code == "MissingSecurityHeader")
			}
			if err != nil || !rejected {
				t.Fatal("unexpected native signature rejection", mutation, status, code)
			}
			after, err := p.store.ObjectState(ctx, bucket, key)
			if err != nil || after.Version != before.Version || after.ETag != before.ETag || after.Size != before.Size {
				t.Fatal("tampered native request changed the stored generation", mutation)
			}
		}
	})
	t.Run("conditional multipart fails closed", func(t *testing.T) {
		_, err := p.CompleteMultipartWithResult(ctx, bucket, MultipartCompleteRequest{}, ObjectWriteConditions{IfNoneMatch: "*"})
		if !errors.Is(err, ErrUnsupported) {
			t.Fatal("conditional multipart capability changed", err)
		}
	})
}
