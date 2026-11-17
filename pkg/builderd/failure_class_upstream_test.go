package builderd

import (
	"strings"
	"testing"
)

func TestUpstreamFetchFailed(t *testing.T) {
	tests := []struct {
		name       string
		log        string
		dockerfile bool
		want       string
	}{
		{
			name: "mise bootstrap 504 (#6666)",
			log: "[build]\n↳ Detected Go\n" +
				"ERRO failed to ensure mise is installed: failed to download and install: temporary failure fetching https://github.com/jdx/mise/releases/download/v2026.8.13/mise-v2026.8.13-linux-x64-musl.tar.gz: unexpected status 504 Gateway Timeout\n",
			want: "github.com/jdx/mise",
		},
		{
			name: "toolchain download refused by nodejs.org",
			log:  "[build]\nmise ERROR HTTP status server error (502 Bad Gateway) for url (https://nodejs.org/dist/v24.11.0/node-v24.11.0-linux-x64.tar.gz)\n",
			want: "nodejs.org/dist",
		},
		{
			name: "Go module proxy 502",
			log:  "[build]\ngo: github.com/acme/lib@v1.2.3: reading https://proxy.golang.org/github.com/acme/lib/@v/v1.2.3.zip: 502 Bad Gateway\n#12 ERROR: process \"/bin/sh -c go build ./...\" did not complete successfully: exit code: 1\n",
			want: "proxy.golang.org",
		},
		{
			name: "npm registry DNS failure",
			log:  "[build]\nnpm error code EAI_AGAIN\nnpm error network request to https://registry.npmjs.org/express failed, reason: getaddrinfo EAI_AGAIN registry.npmjs.org\n",
			want: "registry.npmjs.org",
		},
		{
			name: "Railpack builder image token request times out (escaped repository)",
			log:  "[build]\n" + `error: failed to solve: DeadlineExceeded: failed to fetch anonymous token: Get "https://ghcr.io/token?scope=repository%3Arailwayapp%2Frailpack-builder%3Apull&service=ghcr.io": dial tcp: lookup ghcr.io: i/o timeout` + "\n",
			want: "ghcr.io/railwayapp",
		},
		{
			name: "Go module the customer named does not exist",
			log:  "[build]\ngo: github.com/acme/lib@v9.9.9: reading https://proxy.golang.org/github.com/acme/lib/@v/v9.9.9.info: 404 Not Found\n\tserver response: not found: unknown revision v9.9.9\n",
			want: "",
		},
		{
			name: "npm package the customer named does not exist",
			log:  "[build]\nnpm error code E404\nnpm error 404 Not Found - GET https://registry.npmjs.org/no-such-pkg - Not found\n",
			want: "",
		},
		{
			name: "retried download then the customer's code failed",
			log: "[build]\nWARNING: Retrying (Retry(total=4)) after connection broken by 'ReadTimeoutError(\"HTTPSConnectionPool(host='files.pythonhosted.org', port=443): Read timed out. (read timeout=15)\")': /packages/x.whl\n" +
				"Successfully installed flask-3.1.0\n  File \"/app/main.py\", line 3\nSyntaxError: invalid syntax\n",
			want: "",
		},
		{
			name: "the customer's own service is down",
			log:  "[build]\nError: request to https://api.acme.com/config failed: 503 Service Unavailable\n",
			want: "",
		},
		{
			name:       "Dockerfile build fetching a toolchain",
			log:        "[build]\n#7 [2/5] RUN curl -fsSL https://nodejs.org/dist/v24.11.0/node.tar.gz: 504 Gateway Timeout\n",
			dockerfile: true,
			want:       "",
		},
		{
			name: "only buildkitd's daemon log names the host",
			log:  "[build]\nTypeError: x is not a function\n\n[buildkitd]\nlookup registry.npmjs.org: i/o timeout\n",
			want: "",
		},
		{
			name: "fetch failure far above the step that failed the build",
			log: "[build]\ngo: reading https://proxy.golang.org/x/@v/list: 502 Bad Gateway\n" +
				strings.Repeat("compiling...\n", upstreamFetchTailLines) + "main.go:3: undefined: foo\n",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstreamFetchFailed(tc.log, tc.dockerfile); got != tc.want {
				t.Fatalf("upstreamFetchFailed = %q, want %q", got, tc.want)
			}
		})
	}
}
