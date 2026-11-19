// adr: 20261118203000 — off-host backup retention is the bucket's lifecycle.
package daemonunitspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The hosts push backups with an append-only identity, so the bucket's
// lifecycle policy is the only thing that ever deletes a recovery point.
// The GCS and S3 renderings must carry the same policy, cover exactly the
// prefixes the backup units write, and never expire WAL before the base
// backups it replays onto.
func TestBackupLifecycleMatchesTheBackupUnits(t *testing.T) {
	root := repoRoot(t)
	read := func(rel string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	match := func(rel, pattern string) string {
		t.Helper()
		m := regexp.MustCompile(pattern).FindStringSubmatch(read(rel))
		if m == nil {
			t.Fatalf("%s: no match for %s", rel, pattern)
		}
		return m[1] + "/"
	}
	base := match("deploy/systemd/faas-pg-basebackup-push.service", `(?m)^Environment=OFF_HOST_BACKUP_BASEBACKUP_PATH=(\S+)$`)
	wal := match("deploy/systemd/faas-pg-wal-prune.service", `(?m)^Environment=OFF_HOST_BACKUP_WAL_PATH=(\S+)$`)
	state := match("deploy/ansible/roles/cp_state_backup/defaults/main.yml", `(?m)^faas_cp_state_backup_path: (\S+)$`)
	archive := match("deploy/ansible/roles/postgres/tasks/main.yml", `off_host_backup_wal_path \| default\('([^']+)', true\)`)
	if archive != wal {
		t.Fatalf("archive_command writes WAL under %s but the prune unit reads %s", archive, wal)
	}

	gcs := gcsExpiryDays(t, read("deploy/backup-lifecycle/gcs.json"))
	s3 := s3ExpiryDays(t, read("deploy/backup-lifecycle/s3.json"))
	for name, days := range map[string]map[string]int{"gcs.json": gcs.expiry, "s3.json": s3.expiry} {
		if len(days) != 3 {
			t.Errorf("%s expires %v, want exactly %s, %s and %s", name, days, base, wal, state)
		}
		for _, prefix := range []string{base, wal, state} {
			if days[prefix] == 0 {
				t.Errorf("%s has no expiry for %s, so it grows without bound", name, prefix)
			}
		}
		if days[base] < 7 {
			t.Errorf("%s keeps base backups %d days, want at least a week of recovery points", name, days[base])
		}
		// A base backup lives up to its expiry day before the asynchronous
		// lifecycle run removes it; its WAL must outlive it by a day of
		// that lag plus the day the backup itself spans.
		if days[wal] < days[base]+2 {
			t.Errorf("%s expires WAL after %d days but base backups after %d: the oldest base backup could not be replayed", name, days[wal], days[base])
		}
		if days[state] != days[base] {
			t.Errorf("%s keeps control-plane state %d days and base backups %d: a restore needs both from the same night", name, days[state], days[base])
		}
	}
	for prefix, days := range gcs.expiry {
		if s3.expiry[prefix] != days {
			t.Errorf("%s: gcs.json expires after %d days, s3.json after %d", prefix, days, s3.expiry[prefix])
		}
	}
	if gcs.abortDays == 0 || gcs.abortDays != s3.abortDays {
		t.Errorf("unfinished multipart uploads: gcs.json aborts after %d days, s3.json after %d; both must abort them", gcs.abortDays, s3.abortDays)
	}
}

type lifecycleDays struct {
	expiry    map[string]int // prefix -> days until the current object is deleted
	abortDays int
}

func gcsExpiryDays(t *testing.T, body string) lifecycleDays {
	t.Helper()
	var doc struct {
		Rule []struct {
			Action struct {
				Type string `json:"type"`
			} `json:"action"`
			Condition struct {
				Age           int      `json:"age"`
				MatchesPrefix []string `json:"matchesPrefix"`
			} `json:"condition"`
		} `json:"rule"`
	}
	decodeStrict(t, "gcs.json", body, &doc)
	out := lifecycleDays{expiry: map[string]int{}}
	for _, r := range doc.Rule {
		switch r.Action.Type {
		case "Delete":
			for _, p := range r.Condition.MatchesPrefix {
				if _, dup := out.expiry[p]; dup {
					t.Errorf("gcs.json: %s has two delete rules", p)
				}
				out.expiry[p] = r.Condition.Age
			}
		case "AbortIncompleteMultipartUpload":
			out.abortDays = r.Condition.Age
		default:
			t.Errorf("gcs.json: unexpected action %q", r.Action.Type)
		}
	}
	return out
}

func s3ExpiryDays(t *testing.T, body string) lifecycleDays {
	t.Helper()
	var doc struct {
		Rules []struct {
			ID     string `json:"ID"`
			Status string `json:"Status"`
			Filter struct {
				Prefix string `json:"Prefix"`
			} `json:"Filter"`
			Expiration *struct {
				Days int `json:"Days"`
			} `json:"Expiration"`
			NoncurrentVersionExpiration *struct {
				NoncurrentDays int `json:"NoncurrentDays"`
			} `json:"NoncurrentVersionExpiration"`
			AbortIncompleteMultipartUpload *struct {
				DaysAfterInitiation int `json:"DaysAfterInitiation"`
			} `json:"AbortIncompleteMultipartUpload"`
		} `json:"Rules"`
	}
	decodeStrict(t, "s3.json", body, &doc)
	out := lifecycleDays{expiry: map[string]int{}}
	for _, r := range doc.Rules {
		if r.Status != "Enabled" {
			t.Errorf("s3.json: rule %s is %q, want Enabled", r.ID, r.Status)
		}
		if r.AbortIncompleteMultipartUpload != nil {
			out.abortDays = r.AbortIncompleteMultipartUpload.DaysAfterInitiation
		}
		if r.Expiration == nil {
			continue
		}
		if r.NoncurrentVersionExpiration == nil {
			t.Errorf("s3.json: rule %s leaves noncurrent versions forever on a versioned bucket", r.ID)
		}
		if strings.TrimSpace(r.Filter.Prefix) == "" {
			t.Errorf("s3.json: rule %s expires every object in the bucket", r.ID)
		}
		if _, dup := out.expiry[r.Filter.Prefix]; dup {
			t.Errorf("s3.json: %s has two expiration rules", r.Filter.Prefix)
		}
		out.expiry[r.Filter.Prefix] = r.Expiration.Days
	}
	return out
}

func decodeStrict(t *testing.T, name, body string, into any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
