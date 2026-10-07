package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type bucketResumeFixture struct {
	transferFixture
	mu              sync.Mutex
	providerParts   map[int32][]byte
	writes          []int
	creates         int
	reads           int
	lists           int
	completes       int
	failSign        int
	lostPart        int
	lostComplete    bool
	pendingComplete bool
	pages           []api.ObjectMultipartPartList
	onSign          func(int)
}

func (c *bucketResumeFixture) GetObjectMultipartUpload(context.Context, string, string, string) (api.ObjectMultipartUpload, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return c.session, nil
}

func (c *bucketResumeFixture) CreateObjectMultipartUpload(ctx context.Context, app, bucket string, req api.CreateObjectMultipartUploadRequest) (api.ObjectMultipartUpload, error) {
	c.creates++
	return c.transferFixture.CreateObjectMultipartUpload(ctx, app, bucket, req)
}

func (c *bucketResumeFixture) ListObjectMultipartParts(_ context.Context, _, _, _ string, marker, _ int) (api.ObjectMultipartPartList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists++
	if c.pages != nil {
		if len(c.pages) == 0 {
			return api.ObjectMultipartPartList{}, errors.New("unexpected extra page")
		}
		page := c.pages[0]
		c.pages = c.pages[1:]
		return page, nil
	}
	// Deliberately return one part per page to exercise reconstruction.
	for part := int32(marker + 1); part <= c.session.PartCount; part++ {
		if body, exists := c.providerParts[part]; exists {
			next := int32(0)
			for p := part + 1; p <= c.session.PartCount; p++ {
				if _, exists := c.providerParts[p]; exists {
					next = part
				}
			}
			return api.ObjectMultipartPartList{Items: []api.ObjectMultipartPart{{PartNumber: part, ETag: fmt.Sprintf(`"part-%d"`, part), SizeBytes: int64(len(body))}}, NextPartNumberMarker: next}, nil
		}
	}
	return api.ObjectMultipartPartList{Items: []api.ObjectMultipartPart{}}, nil
}

func (c *bucketResumeFixture) SignObjectMultipartPart(ctx context.Context, app, bucket, upload string, part int, req api.ObjectMultipartPartSignRequest) (api.ObjectSignedRequest, error) {
	if c.onSign != nil {
		c.onSign(part)
	}
	if part == c.failSign {
		c.failSign = 0
		return api.ObjectSignedRequest{}, errors.New("signing interrupted")
	}
	signed, err := c.transferFixture.SignObjectMultipartPart(ctx, app, bucket, upload, part, req)
	signed.URL += "?part=" + strconv.Itoa(part)
	return signed, err
}

func (c *bucketResumeFixture) CompleteObjectMultipartUpload(_ context.Context, _, _, _ string, req api.CompleteObjectMultipartUploadRequest) (api.ObjectMultipartUpload, error) {
	c.completes++
	if c.pendingComplete {
		c.pendingComplete = false
		c.session.State = "completing"
		c.parts = req.Parts
		return api.ObjectMultipartUpload{}, errors.New("completion journal pending")
	}
	c.parts = req.Parts
	c.session.State, c.session.ETag = "completed", `"completed"`
	if c.lostComplete {
		c.lostComplete = false
		return api.ObjectMultipartUpload{}, errors.New("completion response lost")
	}
	return c.session, nil
}

func newBucketResumeFixture(t *testing.T) (*bucketResumeFixture, bucketTransferOptions) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("abcdefg"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &bucketResumeFixture{providerParts: make(map[int32][]byte)}
	c.session = api.ObjectMultipartUpload{ID: uuid.NewString(), Key: "世界 +%", SizeBytes: 7, PartSizeBytes: 3, PartCount: 3, ContentType: "text/plain", State: "active", ExpiresAt: time.Now().Add(time.Hour), VersionID: uuid.NewString()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		part, _ := strconv.Atoi(r.URL.Query().Get("part"))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		c.providerParts[int32(part)] = body
		c.writes = append(c.writes, part)
		if c.lostPart == part {
			c.lostPart = 0
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("ETag", fmt.Sprintf(`"part-%d"`, part))
	}))
	t.Cleanup(server.Close)
	c.url = server.URL
	return c, bucketTransferOptions{action: "upload", app: "demo", bucket: uuid.NewString(), key: c.session.Key, path: source, contentType: c.session.ContentType}
}

// adr: 639
func TestBucketUploadResumeAfterInterruption(t *testing.T) {
	for _, scenario := range []string{"before part", "lost part acknowledgment", "lost completion acknowledgment"} {
		t.Run(scenario, func(t *testing.T) {
			c, o := newBucketResumeFixture(t)
			switch scenario {
			case "before part":
				c.failSign = 2
			case "lost part acknowledgment":
				c.lostPart = 2
			case "lost completion acknowledgment":
				c.lostComplete = true
			}
			pending, err := runBucketTransfer(t.Context(), c, o)
			if err == nil || pending.Status != "pending" || pending.UploadID != c.session.ID {
				t.Fatal(pending, err)
			}
			o.resumeID = pending.UploadID
			// A fresh client/process retains no in-memory completion parts.
			c.parts = nil
			result, err := runBucketTransfer(t.Context(), c, o)
			if err != nil || result.Status != "completed" || result.UploadID != pending.UploadID || result.VersionID != c.session.VersionID || c.creates != 1 || c.completes != 1 {
				t.Fatal(result, err, c.creates, c.completes)
			}
			want := []int{1, 2, 3}
			if scenario == "lost part acknowledgment" {
				want = []int{1, 2, 2, 3}
			}
			if !reflect.DeepEqual(c.writes, want) {
				t.Fatal("acknowledged parts repeated", c.writes, want)
			}
			payload := bytes.Join([][]byte{c.providerParts[1], c.providerParts[2], c.providerParts[3]}, nil)
			if string(payload) != "abcdefg" {
				t.Fatal("resumed object mixed source bytes", string(payload))
			}
			before := len(c.writes)
			if _, err = runBucketTransfer(t.Context(), c, o); err != nil || len(c.writes) != before || c.completes != 1 {
				t.Fatal("terminal replay mutated the provider", err)
			}
		})
	}
}

// adr: 639
func TestBucketUploadResumeRejectsChangedIdentityBeforeAPICalls(t *testing.T) {
	for _, scenario := range []string{"same-size source", "key", "app", "bucket", "content type", "missing checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			c, o := newBucketResumeFixture(t)
			c.failSign = 2
			pending, err := runBucketTransfer(t.Context(), c, o)
			if err == nil {
				t.Fatal("fixture did not interrupt")
			}
			o.resumeID = pending.UploadID
			switch scenario {
			case "same-size source":
				if err := os.WriteFile(o.path, []byte("ABCDEFG"), 0600); err != nil {
					t.Fatal(err)
				}
			case "key":
				o.key += "changed"
			case "app":
				o.app = "other"
			case "bucket":
				o.bucket = uuid.NewString()
			case "content type":
				o.contentType, o.contentTypeSet = "application/json", true
			case "missing checkpoint":
				o.resumeID = uuid.NewString()
			}
			calls, writes := c.calls, len(c.writes)
			if _, err = runBucketTransfer(t.Context(), c, o); err == nil || c.reads != 0 || c.calls != calls || len(c.writes) != writes {
				t.Fatal("invalid resume contacted API/provider", err, c.reads)
			}
		})
	}
}

// adr: 639
func TestBucketUploadResumeRejectsMalformedPartPages(t *testing.T) {
	for _, scenario := range []string{"wrong size", "wrong ETag", "repeated marker", "out of range", "unattempted", "empty truncated page"} {
		t.Run(scenario, func(t *testing.T) {
			c, o := newBucketResumeFixture(t)
			c.failSign = 2
			pending, err := runBucketTransfer(t.Context(), c, o)
			if err == nil {
				t.Fatal("fixture did not interrupt")
			}
			o.resumeID = pending.UploadID
			part := api.ObjectMultipartPart{PartNumber: 1, ETag: `"part-1"`, SizeBytes: 3}
			page := api.ObjectMultipartPartList{Items: []api.ObjectMultipartPart{part}}
			switch scenario {
			case "wrong size":
				page.Items[0].SizeBytes = 4
			case "wrong ETag":
				page.Items[0].ETag = `"foreign"`
			case "repeated marker":
				page.NextPartNumberMarker = 1
				c.pages = append(c.pages, page)
			case "out of range":
				page.Items[0].PartNumber = 4
			case "unattempted":
				page.Items[0].PartNumber, page.Items[0].SizeBytes = 3, 1
			case "empty truncated page":
				page.Items, page.NextPartNumberMarker = nil, 1
			}
			c.pages = append(c.pages, page)
			writes := len(c.writes)
			if _, err = runBucketTransfer(t.Context(), c, o); err == nil || len(c.writes) != writes || c.completes != 0 {
				t.Fatal("malformed page admitted a write", err)
			}
		})
	}
}

// adr: 639
func TestBucketUploadStagesFingerprintBoundParts(t *testing.T) {
	c, o := newBucketResumeFixture(t)
	c.onSign = func(part int) {
		if part == 1 {
			if err := os.WriteFile(o.path, []byte("ABCDEFG"), 0600); err != nil {
				t.Error(err)
			}
		}
	}
	result, err := runBucketTransfer(t.Context(), c, o)
	if err == nil || result.Status != "pending" || string(c.providerParts[1]) != "abc" || c.completes != 0 || len(c.writes) != 1 {
		t.Fatal("mutable source reached completion", result, err, c.writes)
	}
}

// adr: 639
func TestBucketUploadResumeRejectsTerminalAndChangedSessions(t *testing.T) {
	for _, scenario := range []string{"aborted", "aborting", "expired", "key", "geometry", "content type", "foreign completion"} {
		t.Run(scenario, func(t *testing.T) {
			c, o := newBucketResumeFixture(t)
			c.failSign = 2
			pending, err := runBucketTransfer(t.Context(), c, o)
			if err == nil {
				t.Fatal("fixture did not interrupt")
			}
			o.resumeID = pending.UploadID
			switch scenario {
			case "aborted", "aborting":
				c.session.State = scenario
			case "expired":
				c.session.ExpiresAt = time.Now().Add(-time.Second)
			case "key":
				c.session.Key = "another"
			case "geometry":
				c.session.PartSizeBytes = 4
			case "content type":
				c.session.ContentType = "application/json"
			case "foreign completion":
				c.session.State, c.session.ETag = "completed", `"other"`
			}
			writes := len(c.writes)
			if _, err = runBucketTransfer(t.Context(), c, o); err == nil || len(c.writes) != writes || c.completes != 0 {
				t.Fatal("changed session mutated provider", err)
			}
		})
	}
}

// adr: 639
func TestBucketUploadResumeFlags(t *testing.T) {
	id := uuid.NewString()
	base := []string{"upload", "demo", uuid.NewString(), "key", "source"}
	o, err := parseBucketTransfer(append(base, "--resume="+id, "--content-type=text/plain"))
	if err != nil || o.resumeID != id || !o.contentTypeSet {
		t.Fatal(o, err)
	}
	for _, id := range []string{"private-native-id", uuid.Nil.String(), strings.ToUpper(uuid.NewString())} {
		if _, err := parseBucketTransfer(append(base, "--resume="+id)); err == nil {
			t.Fatal("invalid resume ID accepted", id)
		}
	}
}

// adr: 639
func TestBucketUploadResumePendingCompletionReusesManifest(t *testing.T) {
	c, o := newBucketResumeFixture(t)
	c.pendingComplete = true
	pending, err := runBucketTransfer(t.Context(), c, o)
	if err == nil || pending.Status != "pending" || c.session.State != "completing" {
		t.Fatal(pending, err)
	}
	manifest := append([]api.ObjectMultipartCompletedPart(nil), c.parts...)
	o.resumeID = pending.UploadID
	result, err := runBucketTransfer(t.Context(), c, o)
	if err != nil || result.Status != "completed" || c.lists != 0 || c.creates != 1 || c.completes != 2 || len(c.writes) != 3 || !reflect.DeepEqual(c.parts, manifest) {
		t.Fatal("pending completion repeated transfer or changed manifest", result, err)
	}
}

// adr: 639
func TestBucketUploadResumeAllowsRelocatedIdenticalSource(t *testing.T) {
	c, o := newBucketResumeFixture(t)
	c.failSign = 2
	pending, err := runBucketTransfer(t.Context(), c, o)
	if err == nil {
		t.Fatal("fixture did not interrupt")
	}
	moved := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(o.path, moved); err != nil {
		t.Fatal(err)
	}
	o.path, o.resumeID, o.contentType = moved, pending.UploadID, "application/octet-stream"
	if result, err := runBucketTransfer(t.Context(), c, o); err != nil || result.Status != "completed" || c.creates != 1 {
		t.Fatal(result, err)
	}
}
