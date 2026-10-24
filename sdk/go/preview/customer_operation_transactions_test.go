package faas

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCustomerOperationReceiptContext(t *testing.T) {
	var fixture struct {
		Headers map[string]string `json:"headers"`
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Body    string            `json:"body_base64"`
		Digest  string            `json:"digest"`
	}
	raw, err := os.ReadFile("testdata/customer-operation-request.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	body, err := base64.StdEncoding.DecodeString(fixture.Body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(fixture.Method, fixture.Path, bytes.NewReader(body))
	for name, value := range fixture.Headers {
		r.Header.Set(name, value)
	}
	input, err := CustomerOperationRequestFromHTTP(r, body)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := CustomerOperationRequestDigest(input)
	if err != nil || hex.EncodeToString(digest) != fixture.Digest {
		t.Fatalf("customer digest %x: %v", digest, err)
	}
	r.Header.Set("X-Gregale-Operation-Attempt", "2")
	r.Header.Set("X-Gregale-Operation-Capability", strings.Repeat("c", 64))
	r.Header.Set("X-Faas-Invocation-Id", "ffffffff-6666-4666-8666-ffffffffffff")
	later, err := CustomerOperationRequestFromHTTP(r, body)
	if err != nil {
		t.Fatal(err)
	}
	laterDigest, err := CustomerOperationRequestDigest(later)
	if err != nil || !bytes.Equal(digest, laterDigest) {
		t.Fatal("claim changed receipt identity")
	}
	for name, value := range map[string]string{
		"X-Gregale-Customer-Operation-Receipt-Version": "2", "X-Gregale-Customer-Operation-Receipt-Binding": "bad",
		"X-Faas-Platform-Tenant-Id": "", "X-Faas-Invocation-Id": "", "X-Gregale-Operation-Attempt": "01",
		"X-Gregale-Operation-Capability": "bad", "X-Gregale-Operation-Execution-Kind": "workflow", "X-Gregale-Operation-Result-Version": "1",
	} {
		bad := r.Clone(context.Background())
		bad.Header.Set(name, value)
		if _, err := CustomerOperationRequestFromHTTP(bad, body); !errors.Is(err, ErrInvalidOperationRequest) {
			t.Fatalf("accepted %s: %v", name, err)
		}
	}
	r.Header.Add("X-Gregale-Customer-Operation-Receipt-Binding", strings.Repeat("b", 64))
	if _, err := CustomerOperationRequestFromHTTP(r, body); !errors.Is(err, ErrInvalidOperationRequest) {
		t.Fatal("accepted duplicate binding")
	}
	if _, err := CustomerOperationRequestDigest(CustomerOperationRequest{}); !errors.Is(err, ErrInvalidOperationRequest) {
		t.Fatal("accepted unnegotiated context")
	}
	if _, err := WithOperationTransaction(context.Background(), nil, input.request, nil); !errors.Is(err, ErrInvalidOperationRequest) {
		t.Fatal("customer context entered managed helper")
	}
}
