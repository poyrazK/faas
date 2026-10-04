package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/api"
)

func pollGatewayWriteReceipt(t *testing.T, f gatewayRecoveryFixture, key, id, status string, code int) {
	t.Helper()
	pollGatewayOperationReceipt(t, f, key, id, status, "copy", code)
}

func pollGatewayOperationReceipt(t *testing.T, f gatewayRecoveryFixture, key, id, status, operation string, code int) {
	t.Helper()
	ctx := t.Context()
	endpoint := *f.client.Options().BaseEndpoint
	r, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/assets/"+key+"?"+url.Values{"gregale-upload-id": {id}}.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	credentials, err := f.client.Options().Credentials.Retrieve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = awsv4.NewSigner().SignHTTP(ctx, credentials, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != code {
		t.Fatal("receipt HTTP status", response.StatusCode, code)
	}
	if code != 200 {
		return
	}
	var receipt api.ObjectWriteReceipt
	if err = json.NewDecoder(response.Body).Decode(&receipt); err != nil || receipt.ID != id || receipt.Status != status || receipt.Key != key || receipt.Operation != operation || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(receipt, err)
	}
}
