package region

import "testing"

func TestArtifactNamespaceAndCompanions(t *testing.T) {
	for _, id := range []string{"", "eu-fsn", "us-east"} {
		key := ArtifactKey(id, "apps/app/dep.ext4")
		gotID, relative, err := SplitArtifactKey(key)
		if err != nil || gotID != id || relative != "apps/app/dep.ext4" {
			t.Fatalf("split %s: %s %s %v", key, gotID, relative, err)
		}
		if got := SignatureKey(key); got != ArtifactKey(id, "sigs/apps/app/dep.ext4.sig") {
			t.Fatal(got)
		}
	}
	for _, key := range []string{"regions", "regions/eu", "regions/Eu/apps/x", "regions/eu/regions/us/apps/x"} {
		if _, _, err := SplitArtifactKey(key); err == nil {
			t.Fatalf("accepted %s", key)
		}
	}
}
