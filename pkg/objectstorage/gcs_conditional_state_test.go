package objectstorage

// adr: 712

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type gcsStateUploadMetadata struct {
	Name         string `json:"name"`
	ContentType  string `json:"contentType"`
	CacheControl string `json:"cacheControl"`
}

func readGCSStateUpload(r *http.Request) (gcsStateUploadMetadata, []byte, error) {
	var metadata gcsStateUploadMetadata
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/related" || params["boundary"] == "" {
		return metadata, nil, errors.New("expected a multipart JSON API upload")
	}
	parts := multipart.NewReader(r.Body, params["boundary"])
	part, err := parts.NextPart()
	if err != nil {
		return metadata, nil, err
	}
	if err := json.NewDecoder(part).Decode(&metadata); err != nil {
		return metadata, nil, err
	}
	part, err = parts.NextPart()
	if err != nil {
		return metadata, nil, err
	}
	body, err := io.ReadAll(part)
	return metadata, body, err
}

func gcsStateAck(w http.ResponseWriter, body []byte, generation string, size int64) {
	checksum := crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"generation": generation,
		"size":       strconv.FormatInt(size, 10),
		"crc32c":     base64.StdEncoding.EncodeToString(binary.BigEndian.AppendUint32(nil, checksum)),
		"etag":       "same-content",
	})
}

func TestGCSConditionalStateWriteConditionsAndNoRetries(t *testing.T) {
	for _, tc := range []struct {
		name, expected, generation string
		status                     int
		size                       int64
		want                       error
	}{
		{"create", "", "42", http.StatusOK, 2, nil},
		{"replace", "41", "42", http.StatusOK, 2, nil},
		{"empty-body", "", "42", http.StatusOK, 0, nil},
		{"precondition", "41", "", http.StatusPreconditionFailed, 0, ErrPreconditionFailed},
		{"generic-conflict", "41", "", http.StatusConflict, 0, ErrUnavailable},
		{"rate-limit", "", "", http.StatusTooManyRequests, 0, ErrUnavailable},
		{"unavailable", "41", "", http.StatusServiceUnavailable, 0, ErrUnavailable},
		{"forbidden", "", "", http.StatusForbidden, 0, ErrConfiguration},
		{"missing-bucket", "", "", http.StatusNotFound, 0, ErrNotFound},
		{"missing-generation", "", "", http.StatusOK, 2, ErrUnavailable},
		{"invalid-generation", "", "-1", http.StatusOK, 2, ErrUnavailable},
		{"unchanged-generation", "41", "41", http.StatusOK, 2, ErrUnavailable},
		{"wrong-length", "", "42", http.StatusOK, 3, ErrUnavailable},
		{"wrong-checksum", "", "42", http.StatusOK, 2, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			key := "state/a +世界%/manifest.json"
			input := []byte(`{}`)
			if tc.name == "empty-body" {
				input = nil
			}
			provider := NewGCSConditionalFixtureForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				condition := tc.expected
				if condition == "" {
					condition = "0"
				}
				if r.Method != http.MethodPost || r.URL.Query().Get("uploadType") != "multipart" || r.URL.Query().Get("ifGenerationMatch") != condition || r.URL.Query().Get("ifMetagenerationMatch") != "" {
					t.Error("state upload lost its content-generation precondition", r.Method, r.URL.RawQuery)
				}
				metadata, body, err := readGCSStateUpload(r)
				if err != nil || metadata.Name != key || metadata.ContentType != "application/json" || metadata.CacheControl != "no-store" || string(body) != string(input) {
					t.Error("state upload changed metadata or payload", metadata, err)
				}
				if tc.status != http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":"private-provider-detail"}}`, tc.status)
					return
				}
				ackBody := body
				if tc.name == "wrong-checksum" {
					ackBody = []byte("corrupted")
				}
				gcsStateAck(w, ackBody, tc.generation, tc.size)
			}))
			version, err := provider.WriteStateObject(t.Context(), "private", key, input, tc.expected)
			if !errors.Is(err, tc.want) || calls.Load() != 1 || err == nil && version != tc.generation || err != nil && version != "" {
				t.Fatal("conditional write result", version, err, calls.Load())
			}
			if err != nil && strings.Contains(err.Error(), "private-provider-detail") {
				t.Fatal("provider error leaked its response body")
			}
		})
	}
}

func TestGCSConditionalStateReadBindsBodyAndGeneration(t *testing.T) {
	for _, tc := range []struct {
		name, generation, encoding string
		body                       string
		status                     int
		chunked                    bool
		want                       error
	}{
		{"valid", "41", "", `{"count":1}`, http.StatusOK, false, nil},
		{"empty-body", "41", "", "", http.StatusOK, false, nil},
		{"missing-generation", "", "", `{}`, http.StatusOK, false, ErrUnavailable},
		{"zero-generation", "0", "", `{}`, http.StatusOK, false, ErrUnavailable},
		{"negative-generation", "-1", "", `{}`, http.StatusOK, false, ErrUnavailable},
		{"malformed-generation", "not-a-generation", "", `{}`, http.StatusOK, false, ErrUnavailable},
		{"oversized", "41", "", strings.Repeat("x", 17), http.StatusOK, false, ErrUnavailable},
		{"unknown-length", "41", "", `{}`, http.StatusOK, true, ErrUnavailable},
		{"encoded-body", "41", "gzip", `{}`, http.StatusOK, false, ErrUnavailable},
		{"wrong-checksum", "41", "", `{}`, http.StatusOK, false, ErrUnavailable},
		{"missing", "", "", "", http.StatusNotFound, false, ErrNotFound},
		{"forbidden", "", "", "", http.StatusForbidden, false, ErrConfiguration},
		{"unavailable", "", "", "", http.StatusServiceUnavailable, false, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			provider := NewGCSConditionalFixtureForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Query().Get("alt") != "media" {
					t.Error("state read split content from its generation")
				}
				if tc.status != http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":"private-provider-detail"}}`, tc.status)
					return
				}
				w.Header().Set("X-Goog-Generation", tc.generation)
				w.Header().Set("X-Goog-Metageneration", "7")
				w.Header().Set("ETag", "unrelated-http-etag")
				w.Header().Set("Content-Encoding", tc.encoding)
				if tc.name == "wrong-checksum" {
					w.Header().Set("X-Goog-Hash", "crc32c=AAAAAA==")
				}
				if tc.chunked {
					w.(http.Flusher).Flush()
				} else {
					w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			body, version, err := provider.ReadStateObject(t.Context(), "private", "state/manifest", 16)
			if !errors.Is(err, tc.want) || calls.Load() != 1 || err == nil && (string(body) != tc.body || version != tc.generation) {
				t.Fatal("conditional read result", string(body), version, err, calls.Load())
			}
			if err != nil && (version != "" || len(body) != 0 || strings.Contains(err.Error(), "private-provider-detail")) {
				t.Fatal("failed state read leaked data or provider details")
			}
		})
	}
}

func TestGCSConditionalStateRejectsInvalidInputsBeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	provider := NewGCSConditionalFixtureForTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	for _, version := range []string{"0", "-1", "+1", "01", " 1", "1 ", `"etag"`, "9223372036854775808"} {
		if _, err := provider.WriteStateObject(t.Context(), "private", "state/manifest", nil, version); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted a noncanonical content generation", version, err)
		}
	}
	for _, limit := range []int64{-1, 0, math.MaxInt64} {
		if _, _, err := provider.ReadStateObject(t.Context(), "private", "state/manifest", limit); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted an unsafe read bound", limit, err)
		}
	}
	for _, input := range []struct{ bucket, key string }{{"", "state/manifest"}, {"private", ""}} {
		if _, _, err := provider.ReadStateObject(t.Context(), input.bucket, input.key, 16); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted an invalid read address", err)
		}
		if _, err := provider.WriteStateObject(t.Context(), input.bucket, input.key, nil, ""); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted an invalid write address", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached GCS", calls.Load())
	}
}

func TestGCSConditionalStateRequiresNativeStoreCapability(t *testing.T) {
	provider := testGCS(gcsDefaultEndpoint, &fakeGCSStore{})
	if _, _, err := provider.ReadStateObject(t.Context(), "private", "state/manifest", 16); !errors.Is(err, ErrUnsupported) {
		t.Fatal("missing native reads did not fail closed", err)
	}
	if _, err := provider.WriteStateObject(t.Context(), "private", "state/manifest", nil, ""); !errors.Is(err, ErrUnsupported) {
		t.Fatal("missing native writes did not fail closed", err)
	}
}
