// adr: 20261019195000
package frameworkprofile

import "testing"

func TestRequiresSourceImageHealthcheck(t *testing.T) {
	const captured = `"image_healthcheck":{"version":"v1","override":{"test":["CMD","/check"]}}`
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{``, false},
		{`{bad`, false},
		{`{"version":"v1","image_healthcheck_required":true}`, false},
		{`{"version":"v1",` + captured + `}`, false},
		{`{"version":"v1",` + captured + `,"image_healthcheck_required":false}`, false},
		{`{"version":"v1",` + captured + `,"image_healthcheck_required":true}`, true},
		{`{"version":"v1",` + captured + `,"image_healthcheck_required":"yes"}`, true},
	} {
		if got := RequiresSourceImageHealthcheck([]byte(tc.raw)); got != tc.want {
			t.Errorf("profile %s: requires=%v want %v", tc.raw, got, tc.want)
		}
	}
}
