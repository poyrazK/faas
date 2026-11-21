package daemonunitspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// ovhS3Action is the action list OVH's policy import accepted on
// 2026-11-21. Anything else is refused at import ("feature not implemented
// yet"), so a policy that names it cannot be applied.
var ovhS3Action = regexp.MustCompile(`^s3:(AbortMultipartUpload|BypassGovernanceRetention|CreateBucket|DeleteBucket|DeleteBucketTagging|DeleteBucketWebsite|DeleteIntelligentTieringConfiguration|DeleteObject|DeleteObjectTagging|GetBucketAcl|GetBucketCORS|GetBucketLocation|GetBucketLogging|GetBucketObjectLockConfiguration|GetBucketTagging|GetBucketVersioning|GetBucketWebsite|GetEncryptionConfiguration|GetIntelligentTieringConfiguration|GetLifecycleConfiguration|GetObject|GetObjectAcl|GetObjectAttributes|GetObjectLegalHold|GetObjectRetention|GetObjectTagging|GetReplicationConfiguration|ListAllMyBuckets|ListBucket|ListBucketMultipartUploads|ListBucketVersions|ListMultipartUploadParts|PutBucketAcl|PutBucketCORS|PutBucketLogging|PutBucketObjectLockConfiguration|PutBucketTagging|PutBucketVersioning|PutBucketWebsite|PutEncryptionConfiguration|PutIntelligentTieringConfiguration|PutLifecycleConfiguration|PutObject|PutObjectAcl|PutObjectLegalHold|PutObjectRetention|PutObjectTagging|PutReplicationConfiguration|RestoreObject)$|^s3:.*\*$`)

type ovhPolicy struct {
	Statement []struct {
		Effect   string
		Action   []string
		Resource []string
	}
}

// TestOVHCustomerObjectStoragePoliciesSeparateBucketAdmin pins the two
// customer storage keys (ADR-20261031122757): only the admin key manages
// buckets, both stay on customer buckets (gregale-*), and OVH can import
// every action named.
func TestOVHCustomerObjectStoragePoliciesSeparateBucketAdmin(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "deploy/ovh/customer-object-storage-policies")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if want := []string{"gregale-customer-admin.json", "gregale-customer-gateway.json"}; !slices.Equal(names, want) {
		t.Fatalf("policies = %v, want %v", names, want)
	}
	customer := map[string]bool{"arn:aws:s3:::gregale-*": true, "arn:aws:s3:::gregale-*/*": true}
	forbiddenForGateway := map[string]bool{
		"s3:CreateBucket": true, "s3:DeleteBucket": true, "s3:PutBucketCORS": true,
		"s3:PutBucketPolicy": true, "s3:PutBucketAcl": true,
	}
	gatewayNeeds := []string{
		"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket", "s3:ListBucketVersions",
		"s3:ListBucketMultipartUploads", "s3:ListMultipartUploadParts", "s3:AbortMultipartUpload",
	}
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var p ovhPolicy
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		gateway := name == "gregale-customer-gateway.json"
		granted := map[string]bool{}
		for _, st := range p.Statement {
			if st.Effect != "Allow" {
				t.Errorf("%s: effect %q", name, st.Effect)
			}
			listOnly := len(st.Action) == 1 && st.Action[0] == "s3:ListAllMyBuckets"
			for _, r := range st.Resource {
				if !customer[r] && !(listOnly && !gateway && r == "arn:aws:s3:::*") {
					t.Errorf("%s: resource %q is outside the customer buckets", name, r)
				}
			}
			for _, a := range st.Action {
				granted[a] = true
				if !ovhS3Action.MatchString(a) {
					t.Errorf("%s: OVH refuses action %q at import", name, a)
				}
				if gateway && (forbiddenForGateway[a] || strings.Contains(a, "*")) {
					t.Errorf("%s: the gateway key may not hold %q", name, a)
				}
			}
		}
		if gateway {
			for _, a := range gatewayNeeds {
				if !granted[a] {
					t.Errorf("%s: missing %q", name, a)
				}
			}
		}
	}
}
