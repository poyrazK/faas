package dataapiinspect

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseObservationRequiresCompleteBoundedEvidence(t *testing.T) {
	valid := Observation{Version: 1, Role: Role{Name: "role", Database: "database"}, Relation: Relation{Name: "notes", Kind: "r"}, Columns: []Column{{Name: "id"}}, Policies: []Policy{}, Triggers: []Trigger{}, TargetDigest: strings.Repeat("a", 64), TrustBundleSHA256: strings.Repeat("b", 64)}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ParseObservation(encoded, "notes"); err != nil || got.TargetDigest != valid.TargetDigest || got.TrustBundleSHA256 != valid.TrustBundleSHA256 {
		t.Fatalf("valid observation: %+v %v", got, err)
	}
	raw := string(encoded)
	cases := map[string]string{
		"duplicate":        strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1),
		"nested duplicate": strings.Replace(raw, `"name":"role"`, `"name":"role","name":"other"`, 1),
		"unknown":          strings.Replace(raw, `"version":1`, `"version":1,"extra":true`, 1),
		"wrong case":       strings.Replace(raw, `"version":1`, `"Version":1`, 1),
		"missing boolean":  strings.Replace(raw, `,"superuser":false`, ``, 1),
		"null boolean":     strings.Replace(raw, `"superuser":false`, `"superuser":null`, 1),
		"null array":       strings.Replace(raw, `"policies":[]`, `"policies":null`, 1),
		"wrong table":      strings.Replace(raw, `"name":"notes"`, `"name":"other"`, 1),
		"bad target":       strings.Replace(raw, valid.TargetDigest, strings.Repeat("X", 64), 1),
		"missing trust":    strings.Replace(raw, `,"trust_bundle_sha256":"`+valid.TrustBundleSHA256+`"`, ``, 1),
		"trailing JSON":    raw + `{}`,
		"oversized":        raw + strings.Repeat(" ", 65536),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseObservation([]byte(content), "notes"); err == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
	duplicateColumn := valid
	duplicateColumn.Columns = []Column{{Name: "id"}, {Name: "id"}}
	duplicate, _ := json.Marshal(duplicateColumn)
	if _, err := ParseObservation(duplicate, "notes"); err == nil {
		t.Fatal("accepted duplicate columns")
	}
	if _, err := ParseObservation(encoded, "notes;drop"); err == nil {
		t.Fatal("accepted invalid selected table")
	}
}
