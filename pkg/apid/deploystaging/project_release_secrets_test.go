package deploystaging

import (
	"bytes"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectReleaseSecretSealing(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	apps := []state.App{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", AccountID: "account", WorkloadName: "api"}}
	req := api.ProjectReleaseSecretStageRequest{RequestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Workloads: map[string]map[string]string{"api": {"TOKEN": "private-credential"}}}
	seal := func(key, value string) ([]byte, string, string, error) {
		cipher, err := secretbox.SealOne(identity.Recipient(), key, value, 4096)
		return cipher, identity.Recipient().String(), "aaaaaaaabbbbbbbb", err
	}
	rows, hash, err := SealProjectReleaseSecrets("account", apps, req, api.MustLimitsFor(api.PlanPro), []byte("host-hmac"), seal)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || bytes.Contains(rows[0].Ciphertext, []byte("private-credential")) {
		t.Fatal("secret staging was not encrypted")
	}
	env, err := secretbox.Open(identity, rows[0].Ciphertext)
	if err != nil || env["TOKEN"] != "private-credential" {
		t.Fatal("sealed credential does not round trip")
	}
	_, replay, err := SealProjectReleaseSecrets("account", apps, req, api.MustLimitsFor(api.PlanPro), []byte("host-hmac"), seal)
	if err != nil || replay != hash {
		t.Fatal("same logical input changed staging fingerprint")
	}
	req.Workloads["api"]["TOKEN"] = "different-private-credential"
	_, changed, err := SealProjectReleaseSecrets("account", apps, req, api.MustLimitsFor(api.PlanPro), []byte("host-hmac"), seal)
	if err != nil || changed == hash {
		t.Fatal("changed secret reused staging fingerprint")
	}
	if _, _, err := SealProjectReleaseSecrets("other", apps, req, api.MustLimitsFor(api.PlanPro), []byte("host-hmac"), seal); err == nil {
		t.Fatal("foreign workload was sealed")
	}
}
