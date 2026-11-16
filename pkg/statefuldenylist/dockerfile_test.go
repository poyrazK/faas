package statefuldenylist

import "testing"

func TestMatchDockerfile(t *testing.T) {
	cases := []struct {
		name, dockerfile, wantBase string
	}{
		// The 2026-11-15 agent eval deployed Redis with this one-liner after
		// `deploy --image redis` was refused.
		{"one-line redis", "FROM redis:7-alpine\nCMD [\"redis-server\", \"--save\", \"\"]\n", "redis:7-alpine"},
		{"registry and namespace", "FROM --platform=linux/amd64 docker.io/bitnami/postgresql:16\nFROM docker.io/library/postgres:16\n", "docker.io/library/postgres:16"},
		{"tag and digest", "FROM postgres:16@sha256:abc\n", "postgres:16@sha256:abc"},
		{"alias resolves", "FROM mongo:7 AS base\nRUN true\nFROM base\n", "mongo:7"},
		{"lowercase from and continuation", "from \\\n  mysql:8\n", "mysql:8"},
		{"build stage only", "FROM postgres:16 AS client\nFROM node:22-slim\nCOPY --from=client /usr/bin/psql /usr/bin/psql\n", ""},
		{"lookalike name", "FROM mongo-express:1\n", ""},
		{"fork under own namespace", "FROM ghcr.io/me/redis-fork:v1\n", ""},
		{"build arg", "ARG BASE=redis:7\nFROM ${BASE}\n", ""},
		{"scratch", "FROM scratch\nCOPY app /app\n", ""},
		{"comment only", "# FROM redis\nFROM node:22\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, hint, denied := MatchDockerfile([]byte(tc.dockerfile))
			if base != tc.wantBase || denied != (tc.wantBase != "") || (denied && hint == "") {
				t.Fatalf("MatchDockerfile = (%q, %q, %v), want base %q", base, hint, denied, tc.wantBase)
			}
		})
	}
}
