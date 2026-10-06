package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type writeCLIClient struct {
	calls    int
	statuses []string
	err      error
}

func (c *writeCLIClient) GetObjectWriteReceipt(_ context.Context, _, _, id string) (api.ObjectWriteReceipt, error) {
	if c.err != nil {
		return api.ObjectWriteReceipt{}, c.err
	}
	status := c.statuses[min(c.calls, len(c.statuses)-1)]
	c.calls++
	return api.ObjectWriteReceipt{ID: id, Status: status}, nil
}
func (*writeCLIClient) ListObjectWriteReceipts(context.Context, string, string, string, int, string) (api.ObjectWriteReceiptList, error) {
	return api.ObjectWriteReceiptList{}, nil
}

func TestBucketWriteWait(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		wait     bool
		want     string
		calls    int
	}{{"completed", []string{"pending", "completed"}, true, "completed", 2}, {"failed", []string{"pending", "failed"}, true, "failed", 2}, {"status reads once", []string{"pending", "completed"}, false, "pending", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			c := &writeCLIClient{statuses: tc.statuses}
			action := "status"
			if tc.wait {
				action = "wait"
			}
			r, err := readBucketWrite(t.Context(), c, bucketWritesOptions{action: action, id: "receipt", interval: time.Millisecond})
			if err != nil || r.Status != tc.want || c.calls != tc.calls {
				t.Fatal(r, c.calls, err)
			}
		})
	}
	c := &writeCLIClient{statuses: []string{"pending"}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	r, err := readBucketWrite(ctx, c, bucketWritesOptions{action: "wait", id: "receipt", interval: time.Hour})
	if !errors.Is(err, context.DeadlineExceeded) || r.Status != "pending" || c.calls != 1 {
		t.Fatal("wait timeout discarded pending receipt", r, err)
	}
	ctx, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	_, err = readBucketWrite(ctx, c, bucketWritesOptions{action: "wait", id: "receipt"})
	if !errors.Is(err, context.Canceled) || c.calls != 1 {
		t.Fatal("cancelled wait polled", c.calls, err)
	}
}

func TestBucketWritesCLI(t *testing.T) {
	bucket, id := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		action, status string
		code           int
	}{{"list", "pending", 0}, {"status", "pending", 0}, {"wait", "completed", 0}, {"wait", "failed", 1}} {
		t.Run(tc.action+tc.status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "/v1/apps/demo/buckets/" + bucket + "/write-receipts"
				if tc.action != "list" {
					want += "/" + id
				}
				if r.Method != "GET" || r.URL.Path != want {
					t.Error(r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				receipt := api.ObjectWriteReceipt{ID: id, Status: tc.status, Operation: "copy"}
				if tc.action == "list" {
					_ = json.NewEncoder(w).Encode(api.ObjectWriteReceiptList{Items: []api.ObjectWriteReceipt{receipt}, NextCursor: "next"})
				} else {
					_ = json.NewEncoder(w).Encode(receipt)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			var out, stderr bytes.Buffer
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &out, &stderr, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			args := []string{"writes", tc.action, "demo", bucket}
			if tc.action != "list" {
				args = append(args, id)
			}
			if code := cmdBucket(args); code != tc.code {
				t.Fatal(code, out.String(), stderr.String())
			}
			if tc.action == "list" {
				var page api.ObjectWriteReceiptList
				if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.NextCursor != "next" {
					t.Fatal(out.String(), err)
				}
			} else {
				var receipt api.ObjectWriteReceipt
				if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.Status != tc.status {
					t.Fatal(out.String(), err)
				}
			}
		})
	}
	for _, args := range [][]string{{"list", "demo", bucket, "--limit=0"}, {"list", "demo", bucket, "--status=bad"}, {"wait", "demo", bucket, id, "--timeout=0s"}, {"wait", "demo", bucket, id, "--poll-interval=1ms"}, {"status", "demo", bucket, "bad"}, {"list", "demo", bucket, "unexpected"}} {
		if _, err := parseBucketWrites(args); err == nil {
			t.Fatal("invalid options accepted", args)
		}
	}
}
