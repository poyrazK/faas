package builderd

import "testing"

func TestPlatformBaseFetchFailed(t *testing.T) {
	const base = "ghcr.io/poyrazk/base-minimal@sha256:b650e4768b0eccbe43c2f86b5a5c29c93c22591774d9ccd776c9f9bcce52edc4"
	tests := []struct {
		name string
		log  string
		ref  string
		want bool
	}{
		{
			name: "dns timeout on the token request (escaped repository)",
			log: "[build]\n#23 exporting to oci image format\n" +
				`error: failed to solve: DeadlineExceeded: failed to fetch anonymous token: Get "https://ghcr.io/token?scope=repository%3Apoyrazk%2Fbase-minimal%3Apull&service=ghcr.io": dial tcp: lookup ghcr.io: i/o timeout` + "\n",
			ref:  base,
			want: true,
		},
		{
			name: "registry unavailable on the plain repository",
			log:  "[build]\nerror: failed to resolve ghcr.io/poyrazk/base-minimal: 503 Service Unavailable\n",
			ref:  base,
			want: true,
		},
		{
			name: "network failure fetching the customer's own image",
			log:  "[build]\nerror: failed to resolve docker.io/acme/private: dial tcp: lookup docker.io: i/o timeout\n",
			ref:  base,
			want: false,
		},
		{
			name: "platform base mentioned, failure is the customer's command",
			log:  "[build]\n#5 FROM ghcr.io/poyrazk/base-minimal\n#9 npm error code E404\n",
			ref:  base,
			want: false,
		},
		{
			name: "only buildkitd's daemon log names the base",
			log:  "[build]\nnpm error code E404\n\n[buildkitd]\nlookup ghcr.io/poyrazk/base-minimal: i/o timeout\n",
			ref:  base,
			want: false,
		},
		{
			name: "dockerfile build has no runtime base",
			log:  "[build]\nlookup ghcr.io: i/o timeout\n",
			ref:  "",
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := platformBaseFetchFailed(tc.log, tc.ref); got != tc.want {
				t.Fatalf("platformBaseFetchFailed = %v, want %v", got, tc.want)
			}
		})
	}
}
