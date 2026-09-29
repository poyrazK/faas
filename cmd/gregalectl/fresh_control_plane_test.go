package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"filippo.io/age"
)

// apid loads host.age.pub through LoadCredential=cd_host_age_recipient. A
// fresh control plane had host.age (from secrets init) but no host.age.pub,
// so apid exited 243/CREDENTIALS.
func TestWriteHostAgeRecipientPublishesTheRecipient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.age.pub")
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("age1stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // replaces a stale copy, then is idempotent
		if err := writeHostAgeRecipient(path, id); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != id.Recipient().String() {
			t.Fatalf("host.age.pub = %q, want %q", got, id.Recipient().String())
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o444 {
			t.Fatalf("host.age.pub mode %o, want 0444", info.Mode().Perm())
		}
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind: %v", err)
	}
}

// The control-plane units load the fleet's image verify key through
// LoadCredential=cd_sign_pub. Compute joins install it from
// COMPUTE_VERIFY_KEY; the control plane must be given the same key before
// those drop-ins reference it.
func TestCDControlPlaneInstallsTheFleetVerifyKey(t *testing.T) {
	workflow := readWorkflow(t, "cd-controlplane.yml")
	install := strings.Index(workflow, `install -o root -g root -m 0444 "$stage/sign-pub.pem" /etc/faas/secrets/sign-pub.pem`)
	credential := strings.Index(workflow, "LoadCredential=cd_sign_pub:/etc/faas/secrets/sign-pub.pem")
	if install < 0 {
		t.Fatal("cd-controlplane never installs /etc/faas/secrets/sign-pub.pem")
	}
	if credential < 0 || install > credential {
		t.Fatal("sign-pub.pem must be installed before the drop-ins that load it")
	}
	for _, want := range []string{
		"COMPUTE_VERIFY_KEY: ${{ secrets.COMPUTE_VERIFY_KEY }}",
		`"$stage/fleet.age" "$stage/fleet.age.pub" "$stage/sign-pub.pem"`,
		"existing sign-pub.pem differs from COMPUTE_VERIFY_KEY",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("cd-controlplane verify-key staging lost %q", want)
		}
	}
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// apid refuses to boot without a persisted MFA-recovery HMAC key, and cannot
// create one in /var/lib/faas. The role must provision exactly the files
// apid loads, and never replace existing ones.
func TestControlPlaneRoleProvisionsApidHMACKeys(t *testing.T) {
	apid := readRepoFile(t, "cmd", "apid", "main.go")
	role := readRepoFile(t, "deploy", "ansible", "roles", "control_plane_service", "tasks", "main.yml")
	for _, name := range []string{"auditHMACKeyFile", "recoveryHMACKeyFile"} {
		m := regexp.MustCompile(`const ` + name + ` = "(/var/lib/faas/[^"]+)"`).FindStringSubmatch(apid)
		if m == nil {
			t.Fatalf("cmd/apid lost %s", name)
		}
		if !strings.Contains(role, "    - "+filepath.Base(m[1])+"\n") {
			t.Errorf("control_plane_service does not provision %s", m[1])
		}
	}
	for _, want := range []string{
		"creates: /var/lib/faas/{{ item }}",
		"openssl rand -hex 32",
		"chown faas-apid:faas",
		"chmod 0600",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("apid HMAC key provisioning lost %q", want)
		}
	}
}

// meterd reads FAAS_MAIL_TRANSPORT from its per-daemon billing.env, not
// sealed.env. The role's mail-transport check must cover the file meterd
// actually loads.
func TestControlPlaneMailTransportCheckCoversMeterd(t *testing.T) {
	unit := readRepoFile(t, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-meterd.service")
	m := regexp.MustCompile(`(?m)^EnvironmentFile=-?(/etc/faas/secrets/meterd/\S+)$`).FindStringSubmatch(unit)
	if m == nil {
		t.Fatal("faas-meterd.service no longer loads a per-daemon env file")
	}
	role := readRepoFile(t, "deploy", "ansible", "roles", "control_plane_service", "tasks", "main.yml")
	if !strings.Contains(role, "meterd:"+m[1]) {
		t.Errorf("mail-transport check does not cover meterd's %s", m[1])
	}
	if !strings.Contains(role, "apid:/etc/faas/sealed.env") {
		t.Error("mail-transport check no longer covers apid's sealed.env")
	}
}
