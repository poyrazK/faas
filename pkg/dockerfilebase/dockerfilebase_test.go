package dockerfilebase_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dockerfilebase"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dockerfile string
		args       map[string]string
		want       []string
		unresolved int
	}{
		{name: "official image", dockerfile: "FROM node:22-slim\nRUN npm ci\n", want: []string{"docker.io/library/node"}},
		{name: "private registry with digest", dockerfile: "FROM ghcr.io/Acme/base@sha256:0123\n", unresolved: 1},
		{name: "private registry lower case", dockerfile: "FROM GHCR.io/acme/base:1.2@sha256:abc\n", want: []string{"ghcr.io/acme/base"}},
		{name: "registry with port", dockerfile: "FROM registry.example.com:5000/team/app:7\n", want: []string{"registry.example.com:5000/team/app"}},
		{name: "localhost", dockerfile: "FROM localhost/app\n", want: []string{"localhost/app"}},
		{name: "hub user image and alias host", dockerfile: "FROM acme/tool\nFROM index.docker.io/acme/other\n", want: []string{"docker.io/acme/other", "docker.io/acme/tool"}},
		{
			name: "multi-stage, stage refs, scratch and copy from",
			dockerfile: "FROM golang:1.26 AS build\nRUN go build\nFROM scratch\nCOPY --from=build /out /out\n" +
				"COPY --from=0 /a /a\nCOPY --chown=1:1 --from=ghcr.io/acme/assets:3 /x /x\n",
			want: []string{"docker.io/library/golang", "ghcr.io/acme/assets"},
		},
		{
			name:       "run mount from",
			dockerfile: "FROM alpine AS base\nRUN --mount=type=cache,target=/c --mount=type=bind,from=quay.io/acme/tools,source=/t,target=/t make\nRUN --mount=from=base,target=/b true\n",
			want:       []string{"docker.io/library/alpine", "quay.io/acme/tools"},
		},
		{
			name:       "global arg default and override",
			dockerfile: "ARG BASE=ghcr.io/acme/base\nARG TAG=1\nARG OTHER=ghcr.io/acme/default\nFROM ${BASE}:${TAG}\nFROM $OTHER\n",
			args:       map[string]string{"OTHER": "ghcr.io/acme/other:2"},
			want:       []string{"ghcr.io/acme/base", "ghcr.io/acme/other"},
		},
		{name: "arg without value is unresolved", dockerfile: "ARG BASE\nFROM ${BASE}\n", unresolved: 1},
		// BuildKit ignores a build arg the Dockerfile never declares.
		{name: "undeclared build arg", dockerfile: "FROM $UNDECLARED\n", args: map[string]string{"UNDECLARED": "ghcr.io/acme/x"}, unresolved: 1},
		{name: "default and alternate expansion", dockerfile: "ARG A\nARG B=x\nFROM ${A:-ghcr.io/acme/a}\nFROM ${B:+ghcr.io/acme/b}\n", want: []string{"ghcr.io/acme/a", "ghcr.io/acme/b"}},
		{name: "platform args", dockerfile: "FROM --platform=$BUILDPLATFORM ghcr.io/acme/cross-${TARGETARCH}\n", want: []string{"ghcr.io/acme/cross-amd64"}},
		{
			name:       "continuations, comments and blank lines",
			dockerfile: "# syntax=docker/dockerfile:1\nFROM \\\n  # a comment\n\n  ghcr.io/acme/base \\\n  AS b\n",
			want:       []string{"ghcr.io/acme/base"},
		},
		{name: "escape directive", dockerfile: "# escape=`\nFROM `\n  ghcr.io/acme/win\n", want: []string{"ghcr.io/acme/win"}},
		{
			name:       "heredoc bodies are not instructions",
			dockerfile: "FROM alpine\nRUN <<EOF\nFROM ghcr.io/evil/one\nEOF\nCOPY <<-'END' /f\n\tFROM ghcr.io/evil/two\n\tEND\nFROM ghcr.io/acme/next\n",
			want:       []string{"docker.io/library/alpine", "ghcr.io/acme/next"},
		},
		{name: "stage arg reuses global", dockerfile: "ARG IMG=ghcr.io/acme/x\nFROM alpine\nARG IMG\nCOPY --from=$IMG /a /a\n", want: []string{"docker.io/library/alpine", "ghcr.io/acme/x"}},
		{name: "lower-case keywords", dockerfile: "from node\ncopy --from=ghcr.io/acme/y / /\n", want: []string{"docker.io/library/node", "ghcr.io/acme/y"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dockerfilebase.Parse([]byte(tc.dockerfile), tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if got.Repositories == nil {
				got.Repositories = []string{}
			}
			want := tc.want
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got.Repositories, want) || got.Unresolved != tc.unresolved {
				t.Fatalf("Parse = %v (unresolved %d), want %v (unresolved %d)", got.Repositories, got.Unresolved, want, tc.unresolved)
			}
		})
	}
}

func TestParseBounds(t *testing.T) {
	var b strings.Builder
	for i := range dockerfilebase.MaxRepositories + 1 {
		fmt.Fprintf(&b, "FROM ghcr.io/acme/img%d\n", i)
	}
	if _, err := dockerfilebase.Parse([]byte(b.String()), nil); !errors.Is(err, dockerfilebase.ErrTooManyRepositories) {
		t.Fatalf("over the bound = %v, want ErrTooManyRepositories", err)
	}
	if _, err := dockerfilebase.Parse(make([]byte, 1<<20+1), nil); err == nil {
		t.Fatal("an oversized Dockerfile parsed")
	}
}

func TestMerge(t *testing.T) {
	a := dockerfilebase.Result{Repositories: []string{"ghcr.io/a/b", "docker.io/library/node"}, Unresolved: 1}
	b := dockerfilebase.Result{Repositories: []string{"docker.io/library/node", "quay.io/x/y"}, Unresolved: 2}
	got, err := dockerfilebase.Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docker.io/library/node", "ghcr.io/a/b", "quay.io/x/y"}; !reflect.DeepEqual(got.Repositories, want) || got.Unresolved != 3 {
		t.Fatalf("Merge = %+v", got)
	}
}

func TestRepositoryKey(t *testing.T) {
	for _, tc := range []struct{ host, repo, want string }{
		{"registry-1.docker.io", "library/node", "docker.io/library/node"},
		{"Index.Docker.io", "node", "docker.io/library/node"},
		{"GHCR.IO", "acme/base", "ghcr.io/acme/base"},
	} {
		got, err := dockerfilebase.RepositoryKey(tc.host, tc.repo)
		if err != nil || got != tc.want {
			t.Errorf("RepositoryKey(%q, %q) = %q, %v; want %q", tc.host, tc.repo, got, err, tc.want)
		}
	}
	for _, bad := range [][2]string{{"ghcr.io", "Acme/base"}, {"ghcr.io", "../x"}, {"bad host", "x"}, {"ghcr.io", ""}} {
		if _, err := dockerfilebase.RepositoryKey(bad[0], bad[1]); err == nil {
			t.Errorf("RepositoryKey(%q, %q) accepted", bad[0], bad[1])
		}
	}
}
