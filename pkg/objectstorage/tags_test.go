package objectstorage

import (
	"errors"
	"testing"
)

func TestObjectTagsRoundTripAndValidation(t *testing.T) {
	tags := map[string]string{"env": "prod", "team name": "core/api"}
	encoded, err := EncodeObjectTags(tags)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseObjectTags(encoded)
	if err != nil || decoded["team name"] != "core/api" || decoded["env"] != "prod" {
		t.Fatalf("decoded=%v err=%v", decoded, err)
	}
	for _, raw := range []string{"env", "env=prod&env=stage", "bad=%zz"} {
		if _, err := ParseObjectTags(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseObjectTags(%q) err=%v", raw, err)
		}
	}
	if err := ValidateObjectMetadata(ObjectMetadata{Metadata: map[string]string{ReservedObjectTagsMetadataKey: "provider-owned"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reserved metadata key err=%v", err)
	}
}
