package region

import "testing"

func TestSBOMReferenceIdentity(t *testing.T) {
	const id = "550e8400-e29b-41d4-a716-446655440000"
	for _, build := range []string{id, "0123456789abcdef0123456789abcdef"} {
		for _, r := range []string{"", "eu-fsn"} {
			key := BuildSBOMKey(r, build)
			if !ValidBuildSBOMKey(r, build, key) {
				t.Fatalf("canonical key rejected: %s", key)
			}
		}
	}
	for _, key := range []string{BuildSBOMKey("", id), BuildSBOMKey("us-east", id), BuildSBOMKey("eu-fsn", "550e8400-e29b-41d4-a716-446655440001"), "regions/eu-fsn/sboms/../sboms/" + id + ".cdx.json", "regions/eu-fsn/sboms/" + id + ".cdx.json/", "regions/eu-fsn/sources/" + id + ".tar.gz"} {
		if ValidBuildSBOMKey("eu-fsn", id, key) {
			t.Fatalf("alias or foreign key accepted: %s", key)
		}
	}
	for _, build := range []string{"../evil", "550E8400-E29B-41D4-A716-446655440000", "0123456789ABCDEF0123456789ABCDEF"} {
		if ValidBuildSBOMKey("eu-fsn", build, BuildSBOMKey("eu-fsn", build)) {
			t.Fatalf("noncanonical ID accepted: %s", build)
		}
	}
}
