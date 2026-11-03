package gateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func largeUpload(client string, size int) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://example.test/invoke", bytes.NewReader(bytes.Repeat([]byte("x"), size)))
	if client != "" {
		r.Header.Set("X-Forwarded-For", client)
	}
	return r
}

// TestAdmitRequestBodyRefusesPastNodeSpoolBudget: concurrent spooled bodies
// whose sum exceeds the node budget get 503 with Retry-After, and closing an
// admitted body hands its bytes back.
func TestAdmitRequestBodyRefusesPastNodeSpoolBudget(t *testing.T) {
	size := int(requestBodyMemoryThreshold) + 1024
	budget := newRequestBodyBudget(int64(size)*2, 0)

	var held []io.Closer
	for i := range 2 {
		w := httptest.NewRecorder()
		r := largeUpload("", size)
		if admitRequestBodyBudgeted(w, r, int64(size)*4, time.Second, budget, "") {
			t.Fatalf("upload %d refused: %d %s", i, w.Code, w.Body)
		}
		held = append(held, r.Body)
	}
	w := httptest.NewRecorder()
	if !admitRequestBodyBudgeted(w, largeUpload("", size), int64(size)*4, time.Second, budget, "") {
		t.Fatal("third upload admitted past the node budget")
	}
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over budget: %d retry-after=%q, want 503 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
	if got := budget.usedBytes(); got != int64(size)*2 {
		t.Fatalf("budget after a refusal = %d, want %d", got, size*2)
	}

	for _, c := range held {
		_ = c.Close()
	}
	if got := budget.usedBytes(); got != 0 {
		t.Fatalf("budget after closing admitted bodies = %d, want 0", got)
	}
	w = httptest.NewRecorder()
	r := largeUpload("", size)
	if admitRequestBodyBudgeted(w, r, int64(size)*4, time.Second, budget, "") {
		t.Fatalf("upload after release refused: %d %s", w.Code, w.Body)
	}
	_ = r.Body.Close()
}

func TestAdmitRequestBodyCapsSpooledUploadsPerClient(t *testing.T) {
	size := int(requestBodyMemoryThreshold) + 1
	budget := newRequestBodyBudget(1<<40, 1)
	w := httptest.NewRecorder()
	first := largeUpload("198.51.100.7", size)
	if admitRequestBodyBudgeted(w, first, int64(size), time.Second, budget, "") {
		t.Fatalf("first upload refused: %d", w.Code)
	}
	defer func() { _ = first.Body.Close() }()

	w = httptest.NewRecorder()
	if !admitRequestBodyBudgeted(w, largeUpload("198.51.100.7", size), int64(size), time.Second, budget, "") || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("second concurrent upload from one client: %d, want 503", w.Code)
	}
	w = httptest.NewRecorder()
	other := largeUpload("198.51.100.8", size)
	if admitRequestBodyBudgeted(w, other, int64(size), time.Second, budget, "") {
		t.Fatalf("another client's upload refused: %d", w.Code)
	}
	_ = other.Body.Close()
}

// TestAdmitRequestBodyCapsOneOwnersShareOfTheSpool: uploads to one owner's
// apps stop at its share of the node spool while another owner's upload is
// still admitted (H94).
func TestAdmitRequestBodyCapsOneOwnersShareOfTheSpool(t *testing.T) {
	size := int(requestBodyMemoryThreshold) + 1024
	budget := newRequestBodySpoolBudget(int64(size)*api.RequestBodySpoolOwnerShareDivisor, 0)

	w := httptest.NewRecorder()
	first := largeUpload("198.51.100.1", size)
	if admitRequestBodyBudgeted(w, first, int64(size)*4, time.Second, budget, "acct-busy") {
		t.Fatalf("first upload refused: %d %s", w.Code, w.Body)
	}
	defer func() { _ = first.Body.Close() }()

	w = httptest.NewRecorder()
	if !admitRequestBodyBudgeted(w, largeUpload("198.51.100.2", size), int64(size)*4, time.Second, budget, "acct-busy") || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("second upload past the owner's share: %d, want 503", w.Code)
	}

	w = httptest.NewRecorder()
	other := largeUpload("198.51.100.3", size)
	if admitRequestBodyBudgeted(w, other, int64(size)*4, time.Second, budget, "acct-quiet") {
		t.Fatalf("another owner's upload refused while the node budget has room: %d", w.Code)
	}
	_ = other.Body.Close()
}

// TestRequestBodyClientGroupsIPv6By56: two /64s of one /56 share the
// per-client slots; IPv4 stays per address.
func TestRequestBodyClientGroupsIPv6By56(t *testing.T) {
	key := func(xff string) string {
		r := httptest.NewRequest(http.MethodPost, "http://example.test/", nil)
		r.Header.Set("X-Forwarded-For", xff)
		return requestBodyClient(r)
	}
	if a, b := key("2001:db8:1:ab01::1"), key("2001:db8:1:abff::2"); a != b {
		t.Fatalf("two /64s of one /56 got different keys: %q %q", a, b)
	}
	if a, b := key("2001:db8:1:ab01::1"), key("2001:db8:1:ac01::1"); a == b {
		t.Fatalf("two /56s share key %q", a)
	}
	if got := key("198.51.100.7"); got != "198.51.100.7" {
		t.Fatalf("IPv4 key = %q", got)
	}
}

func TestAdmitRequestBodySmallBodiesSkipSpoolBudget(t *testing.T) {
	budget := newRequestBodyBudget(0, 1)
	w := httptest.NewRecorder()
	if admitRequestBodyBudgeted(w, largeUpload("198.51.100.7", 1024), 4096, time.Second, budget, "") {
		t.Fatalf("in-memory body refused by the spool budget: %d", w.Code)
	}
}

func TestHoldValidateBodyChargesHeapBudget(t *testing.T) {
	h := &Handler{bodyMemoryBudget: newRequestBodyBudget(api.RequestBodyMemoryNodeBytes, 0)}
	r := httptest.NewRequest(http.MethodPost, "http://example.test/", nil)
	r.ContentLength = -1
	held := 0
	for {
		if _, ok := h.holdValidateBody(r, 25<<20); !ok {
			break
		}
		held++
		if held > 100 {
			t.Fatal("validate holds never refused")
		}
	}
	if want := int(api.RequestBodyMemoryNodeBytes / (25 << 20)); held != want {
		t.Fatalf("concurrent 25 MiB validate holds = %d, want %d", held, want)
	}
}
