package objectstorage

import "testing"

// adr: 20261107214517
func TestStartsInAllVersionAccounting(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    any
		want bool
	}{
		{"gcs", &GCS{}, true},
		{"s3", &S3{}, false},
		{"nil", nil, false},
	} {
		if got := StartsInAllVersionAccounting(tc.p); got != tc.want {
			t.Errorf("%s: StartsInAllVersionAccounting = %v, want %v", tc.name, got, tc.want)
		}
	}
}
