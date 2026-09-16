package githubd

import (
	"testing"
)

func TestWebhookDeliveryMetadataProjectsSafeIdentity(t *testing.T) {
	push := []byte(`{"ref":"refs/heads/main","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repository":{"full_name":"acme/api"},"installation":{"id":42}}`)
	installID, repo, sha := webhookDeliveryMetadata("push", push)
	if installID != 42 || repo != "acme/api" || sha != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("metadata = (%d, %q, %q)", installID, repo, sha)
	}

	if installID, repo, sha := webhookDeliveryMetadata("push", []byte(`not-json`)); installID != 0 || repo != "" || sha != "" {
		t.Fatalf("malformed metadata = (%d, %q, %q), want empty", installID, repo, sha)
	}
}
