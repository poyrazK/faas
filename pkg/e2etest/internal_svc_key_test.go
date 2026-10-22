package e2etest

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"strings"
	"testing"
)

// The harness key pair must be one schedd can load and gatewayd-internal can
// verify with, and a caller's own internal-svc settings take precedence.
func TestInternalSvcKeyEnvPairsMinterAndVerifier(t *testing.T) {
	env := internalSvcKeyEnv(t, t.TempDir(), nil)
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	raw, err := os.ReadFile(values["FAAS_INTERNAL_SVC_KEY_PATH"])
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil || block.Type != "PRIVATE KEY" {
		t.Fatalf("schedd key: %v", err)
	}
	var pubkeys map[string]string
	if err := json.Unmarshal([]byte(values["FAAS_INTERNAL_SVC_PUBKEYS"]), &pubkeys); err != nil {
		t.Fatal(err)
	}
	pubBlock, _ := pem.Decode([]byte(pubkeys["schedd"]))
	public, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil || pubBlock.Type != "PUBLIC KEY" {
		t.Fatalf("schedd public key: %v", err)
	}
	if !parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey).Equal(public) {
		t.Fatal("published public key does not match the minting key")
	}
	if got := internalSvcKeyEnv(t, t.TempDir(), []string{"FAAS_INTERNAL_SVC_PUBKEYS={}"}); got != nil {
		t.Fatalf("caller override replaced: %v", got)
	}
}
