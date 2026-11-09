// adr: 20261109120000 — slice 2: host secrets and the control plane's Postgres on the encrypted volume.
package daemonunitspec

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The paths that move are the host secrets everywhere, and the Postgres
// cluster, WAL archive and local base backups on the control plane.
func TestEncryptedVolumeHoldsHostSecretsAndPostgres(t *testing.T) {
	var defaults struct {
		Subdir   string                        `yaml:"faas_storage_private_subdir"`
		Secrets  []struct{ Path, Name string } `yaml:"faas_storage_encrypted_secret_paths"`
		Postgres []struct {
			Path, Name string
			Postgres   bool
		} `yaml:"faas_storage_encrypted_postgres_paths"`
	}
	if err := yaml.Unmarshal([]byte(readRepoText(t, "deploy/ansible/roles/storage/defaults/main.yml")), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Subdir != "host-private" || len(defaults.Secrets) != 1 || defaults.Secrets[0].Path != "/etc/faas/secrets" {
		t.Errorf("host secrets must move to <mount>/host-private/: %+v", defaults)
	}
	got := map[string]bool{}
	for _, p := range defaults.Postgres {
		got[p.Path] = p.Postgres
	}
	if !got["/var/lib/postgresql"] || !got["/var/lib/pgsql"] || len(got) != 2 {
		t.Errorf("the Postgres cluster and /var/lib/pgsql (WAL archive, base backups) must move, flagged postgres: %+v", defaults.Postgres)
	}

	main := readRepoText(t, "deploy/ansible/roles/storage/tasks/main.yml")
	include := strings.Index(main, "include_tasks: encrypted_paths.yml")
	final := strings.Index(main, "storage — assert final filesystem contract")
	if include < 0 || final < 0 || include < final {
		t.Fatal("encrypted_paths.yml must run after the volume's final contract is proven")
	}
	if !strings.Contains(main[include:], "'/dev/mapper/' ~ faas_storage_label ~ '-crypt'") {
		t.Error("encrypted_paths.yml must run only when /srv/fc is the opened LUKS volume")
	}
}

func TestEncryptedPathsNeverShredUnprovenDataAndFailClosed(t *testing.T) {
	tasks := readRepoText(t, "deploy/ansible/roles/storage/tasks/encrypted_paths.yml")
	order := []string{
		"refuse to move a Postgres that holds data",
		"refuse to move a running Postgres",
		"copy each path onto the encrypted volume",
		"bind each path from the encrypted volume",
		"shred the plaintext left under each path and seal it",
	}
	last := -1
	for _, name := range order {
		i := strings.Index(tasks, name)
		if i < 0 || i < last {
			t.Fatalf("encrypted_paths.yml must run %q in order %q", name, order)
		}
		last = i
	}
	seal := tasks[strings.Index(tasks, "shred the plaintext left under each path"):]
	steps := []string{
		`grep -Fq "[/{{ faas_storage_private_subdir }}/{{ item.name }}]"`, // the bind comes from the volume
		`"underlay resolves to the volume"`,
		"--checksum --dry-run --itemize-changes",
		"shred -u",
		`chattr +i "$under"`,
	}
	last = -1
	for _, s := range steps {
		i := strings.Index(seal, s)
		if i < 0 || i < last {
			t.Fatalf("seal step %q missing or out of order: the copy must be proven before shredding", s)
		}
		last = i
	}
	for _, want := range []string{
		"faas_storage_pg_databases.stdout | int <= 3",
		"bind,nofail,x-systemd.requires-mounts-for={{ faas_storage_mount }}",
		"RequiresMountsFor=/etc/faas/secrets",
		"/etc/systemd/system/{{ item }}.d/10-faas-encrypted-data.conf",
		`"postgresql@.service"`,
		"ExecStart=/usr/sbin/augenrules --load",
	} {
		if !strings.Contains(tasks, want) {
			t.Errorf("encrypted_paths.yml lost %q", want)
		}
	}
	for _, never := range []string{"state: restarted", "state: stopped", "state: started"} {
		if strings.Contains(tasks, never) {
			t.Errorf("encrypted_paths.yml must never stop or start Postgres (%q)", never)
		}
	}
}
