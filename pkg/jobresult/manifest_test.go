package jobresult

import (
	"strings"
	"testing"
)

func TestValidateOutputManifest(t *testing.T) {
	valid := `{"version":1,"artifacts":[{"name":"partition-a","uri":"s3://results/a.parquet","size_bytes":12,"sha256":"sha256:` + strings.Repeat("a", 64) + `"}]}`
	manifest, err := Validate([]byte(valid))
	if err != nil || len(manifest.Artifacts) != 1 || manifest.Artifacts[0].Name != "partition-a" {
		t.Fatalf("valid manifest = %+v, err %v", manifest, err)
	}
	for _, raw := range []string{
		strings.Replace(valid, "s3://results/a.parquet", "s3://results/a.parquet?token=secret", 1),
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"size_bytes":12`, `"size_bytes":-1`, 1),
		strings.Replace(valid, `"artifacts":[`, `"artifacts":[{"name":"partition-a","uri":"s3://results/a.parquet","size_bytes":12,"sha256":"sha256:`+strings.Repeat("a", 64)+`"},`, 1),
	} {
		if _, err := Validate([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid output manifest: %s", raw)
		}
	}

	withOutcome := `{"version":1,"artifacts":[],"outcome_code":"invalid_record"}`
	manifest, err = Validate([]byte(withOutcome))
	if err != nil || manifest.OutcomeCode != "invalid_record" {
		t.Fatalf("structured outcome = %+v, err %v", manifest, err)
	}
	for _, code := range []string{"Invalid", "bad code", strings.Repeat("a", 65)} {
		raw := `{"version":1,"artifacts":[],"outcome_code":"` + code + `"}`
		if _, err := Validate([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid outcome code %q", code)
		}
	}
}
