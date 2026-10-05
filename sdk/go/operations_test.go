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
	"path/filepath"
	"testing"
)

func TestOperationRequestContract(t *testing.T) {
	var fixture struct {
		Headers map[string]string `json:"headers"`
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Body    string            `json:"body_base64"`
		Digest  string            `json:"digest"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "operation-request.json"))
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
	request := httptest.NewRequest(fixture.Method, fixture.Path, bytes.NewReader(body))
	for name, value := range fixture.Headers {
		request.Header.Set(name, value)
	}
	input, err := OperationRequestFromHTTP(request, body)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := OperationRequestDigest(input)
	if err != nil || hex.EncodeToString(digest) != fixture.Digest {
		t.Fatalf("shared digest %x: %v", digest, err)
	}
	input.Generation = 2
	second, err := OperationRequestDigest(input)
	if err != nil || !bytes.Equal(digest, second) {
		t.Fatalf("generation changed identity: %v", err)
	}
	if _, err := OperationRequestDigest(OperationRequest{}); !errors.Is(err, ErrInvalidOperationRequest) {
		t.Fatal("unnegotiated input accepted")
	}
	for name, value := range map[string]string{
		"X-Gregale-Operation-Result-Version": "2",
		"X-Gregale-Operation-Generation":     "9223372036854775808",
		"X-Faas-App-Id":                      "00000000-0000-0000-0000-000000000000",
	} {
		bad := request.Clone(context.Background())
		bad.Header.Set(name, value)
		if _, err := OperationRequestFromHTTP(bad, body); !errors.Is(err, ErrInvalidOperationRequest) {
			t.Fatalf("invalid %s accepted", name)
		}
	}
	request.Header.Add("X-Gregale-Operation-Id", input.OperationID)
	if _, err := OperationRequestFromHTTP(request, body); !errors.Is(err, ErrInvalidOperationRequest) {
		t.Fatal("duplicate identity accepted")
	}
}
