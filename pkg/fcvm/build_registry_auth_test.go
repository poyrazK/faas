package fcvm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// adr: 20261026090000 — a builder VM gets the built app's registry logins
// as a Docker config in its secrets.env.

func sealRegistryPassword(t *testing.T, m *Manager, ns, password string) []byte {
	t.Helper()
	sealed, err := secretbox.SealBytes(m.registryIdentities[0].Recipient(), ns, []byte(password), 0)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func secretsEnvOf(t *testing.T, files []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	if len(files) > 0 {
		if err := json.Unmarshal(files, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestBuilderSecretsCarryTheRegistryDockerConfig(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetRegistryIdentities([]*age.X25519Identity{newIdentity(t)})
	runtimeID := newIdentity(t)
	m.SetHostIdentity(runtimeID)
	buildSecret := sealEnv(t, runtimeID, secretbox.Envelope{"NPM_TOKEN": "npm"})
	creds := []BuildRegistryCredential{
		{Registry: "ghcr.io", Username: "octo", PasswordSealed: sealRegistryPassword(t, m, registryCredentialNamespace, "gh-pw")},
		{Registry: "other.example", Username: "u", PasswordSealed: sealRegistryPassword(t, m, "app_secret", "wrong-namespace")},
	}

	files, _, err := m.prepareWakeFiles(WakeRequest{ExportDir: "/export", SealedEnvEntries: []SealedEnvEntry{{Key: "NPM_TOKEN", Ciphertext: buildSecret}}, BuildRegistryCredentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	env := secretsEnvOf(t, files)
	if env["NPM_TOKEN"] != "npm" {
		t.Fatalf("build secret lost: %v", env)
	}
	var config struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal([]byte(env[api.BuildRegistryAuthKey]), &config); err != nil {
		t.Fatalf("docker config: %v (%q)", err, env[api.BuildRegistryAuthKey])
	}
	if got, _ := base64.StdEncoding.DecodeString(config.Auths["ghcr.io"].Auth); string(got) != "octo:gh-pw" {
		t.Fatalf("ghcr.io login = %q", got)
	}
	if _, ok := config.Auths["other.example"]; ok {
		t.Fatal("a password sealed under another namespace was opened")
	}

	// Without build secrets, the config alone still makes a secrets.env.
	files, _, err = m.prepareWakeFiles(WakeRequest{ExportDir: "/export", BuildRegistryCredentials: creds[:1]})
	if err != nil || !strings.Contains(secretsEnvOf(t, files)[api.BuildRegistryAuthKey], "ghcr.io") {
		t.Fatalf("builder without secrets: %s, %v", files, err)
	}
}

// Only a builder VM gets registry logins, and a vmmd without the build
// identity leaves the builder anonymous rather than failing it.
func TestRegistryDockerConfigOnlyForBuildersThatCanOpenIt(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetRegistryIdentities([]*age.X25519Identity{newIdentity(t)})
	creds := []BuildRegistryCredential{{Registry: "ghcr.io", Username: "octo", PasswordSealed: sealRegistryPassword(t, m, registryCredentialNamespace, "gh-pw")}}

	if files, _, err := m.prepareWakeFiles(WakeRequest{BuildRegistryCredentials: creds}); err != nil || len(files) != 0 {
		t.Fatalf("app VM got registry logins: %s, %v", files, err)
	}
	m.SetRegistryIdentities(nil)
	if files, _, err := m.prepareWakeFiles(WakeRequest{ExportDir: "/export", BuildRegistryCredentials: creds}); err != nil || len(files) != 0 {
		t.Fatalf("builder without the build identity: %s, %v", files, err)
	}
}
