package api

import (
	"encoding/json"
	"testing"
)

// vmmd and guest-init derive the digest independently; it must not depend on
// map iteration order, and it must change with any value.
func TestRestoreIdentityDocumentIsCanonical(t *testing.T) {
	env := map[string]string{"B": "2", "A": "1"}
	secrets := map[string]string{"DATABASE_URL": "postgres://branch"}
	body, digest, err := RestoreIdentityDocument(env, secrets)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, other, err := RestoreIdentityDocument(map[string]string{"A": "1", "B": "2"}, map[string]string{"DATABASE_URL": "postgres://branch"})
		if err != nil || string(again) != string(body) || other != digest {
			t.Fatalf("document not canonical: %s / %s, %v", again, other, err)
		}
	}
	if !ValidRestoreIdentityDigest(digest) {
		t.Fatalf("digest %q is not valid", digest)
	}
	_, changed, _ := RestoreIdentityDocument(env, map[string]string{"DATABASE_URL": "postgres://parent"})
	if changed == digest {
		t.Fatal("a different secret kept the digest")
	}
	var doc RestoreIdentity
	if err := json.Unmarshal(body, &doc); err != nil || doc.Secrets["DATABASE_URL"] != "postgres://branch" || doc.Env["A"] != "1" {
		t.Fatalf("document = %s, %v", body, err)
	}
	empty, _, _ := RestoreIdentityDocument(nil, nil)
	if string(empty) != `{"env":{},"secrets":{}}` {
		t.Fatalf("empty document = %s", empty)
	}
	for _, bad := range []string{"", "ABC", digest[:63], digest + "0", "g" + digest[1:]} {
		if ValidRestoreIdentityDigest(bad) {
			t.Fatalf("digest %q accepted", bad)
		}
	}
}
