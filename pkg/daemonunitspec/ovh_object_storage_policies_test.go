// adr: 20261118203000 — the backup key is append-only; the bucket lifecycle deletes.
package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The OVH Object Storage user policies are imported by hand, so this is
// the only check that they keep each key to its own bucket and its own
// rights: compute nodes and the backup writer never delete, and no key
// can change a bucket's lifecycle, lock or policy.
func TestOVHObjectStoragePoliciesKeepEachKeyToItsBucket(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "deploy/ovh/object-storage-policies")

	manifest, err := os.ReadFile(filepath.Join(root, "deploy/manifest/production/ovh-live.template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	logBucket := regexp.MustCompile(`(?m)^log_archive:\n(?:  .*\n)*?  bucket: (\S+)$`).FindSubmatch(manifest)
	if logBucket == nil {
		t.Fatal("ovh-live.template.yaml: no log_archive.bucket")
	}

	type want struct {
		bucket string
		delete bool
	}
	wants := map[string]want{
		"gregale-cp-artifacts.json": {"gregale-prod-artifacts", true},
		// The GCP fleet's key while it writes to the OVH bucket (phase 1):
		// CD installs one storage.env on every node and the control plane,
		// so it carries the control plane's rights. Deleted at the cutover.
		"gregale-gcp-fleet-artifacts.json": {"gregale-prod-artifacts", true},
		"gregale-log-archive.json":         {string(logBucket[1]), true},
		"gregale-pg-backups.json":          {"gregale-prod-pg-backups", false},
	}
	computeHosts := regexp.MustCompile(`(?m)^    - name: (\S+)\n      role: compute-only$`).FindAllSubmatch(manifest, -1)
	if len(computeHosts) == 0 {
		t.Fatal("ovh-live.template.yaml: no compute-only hosts")
	}
	for _, m := range computeHosts {
		wants["gregale-"+string(m[1])+"-artifacts.json"] = want{"gregale-prod-artifacts", false}
	}

	readOnly := map[string]bool{
		"s3:GetObject":                  true,
		"s3:PutObject":                  true,
		"s3:ListBucket":                 true,
		"s3:GetBucketLocation":          true,
		"s3:ListBucketMultipartUploads": true,
		"s3:ListMultipartUploadParts":   true,
		"s3:AbortMultipartUpload":       true,
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, path := range files {
		name := filepath.Base(path)
		got = append(got, name)
		w, ok := wants[name]
		if !ok {
			t.Errorf("%s: no key in GcpToOvhMigration.md \"Keys\" or compute host in ovh-live.template.yaml uses this policy", name)
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Statement []struct {
				Sid      string   `json:"Sid"`
				Effect   string   `json:"Effect"`
				Action   []string `json:"Action"`
				Resource []string `json:"Resource"`
			} `json:"Statement"`
		}
		decodeStrict(t, name, string(body), &doc)
		if len(doc.Statement) == 0 {
			t.Errorf("%s: no statements", name)
		}
		deletes := false
		for _, s := range doc.Statement {
			if s.Effect != "Allow" {
				t.Errorf("%s: statement %s is %q; a policy only grants", name, s.Sid, s.Effect)
			}
			for _, r := range s.Resource {
				if r != "arn:aws:s3:::"+w.bucket && r != "arn:aws:s3:::"+w.bucket+"/*" {
					t.Errorf("%s: grants on %s, want only %s and its objects", name, r, w.bucket)
				}
			}
			for _, a := range s.Action {
				switch {
				case readOnly[a]:
				case a == "s3:DeleteObject":
					deletes = true
				default:
					t.Errorf("%s: grants %s; keys never change a bucket's lifecycle, lock, policy or versions", name, a)
				}
			}
		}
		if deletes != w.delete {
			t.Errorf("%s: deletes = %v, want %v (GcpToOvhMigration.md \"Keys\")", name, deletes, w.delete)
		}
	}
	var missing []string
	for name := range wants {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("no policy for %s (have %s)", strings.Join(missing, ", "), strings.Join(got, ", "))
	}
}
