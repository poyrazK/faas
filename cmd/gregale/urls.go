package main

import (
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/wire"
)

// The public web application owns the documentation routes. Keep CLI links
// on the platform host because docs.gregale.dev is not a deployed site.
const (
	docsSiteURL             = "https://" + wire.PlatformHost + "/docs"
	cliDocsURL              = docsSiteURL + "/cli"
	storageDocsURL          = docsSiteURL + "/storage"
	deployFromSourceDocsURL = docsSiteURL + "/deploy-from-source"
)

// docsPageSlugs are the stable, publicly routed pages in faas-web. Command
// topics that do not have their own page use the consolidated CLI reference.
var docsPageSlugs = map[string]struct{}{
	"cli":                    {},
	"compliance":             {},
	"deploy-from-source":     {},
	"dpa":                    {},
	"egress-denylist":        {},
	"preview-environments":   {},
	"responsible-disclosure": {},
	"runtime-go":             {},
	"runtime-node":           {},
	"runtime-python":         {},
	"scale-to-zero":          {},
	"storage":                {},
	"subprocessors":          {},
	"tracing":                {},
}

// docsURLForTopic resolves a user-facing docs topic to a route that the
// public web application serves. The CLI manifest contains many command
// topics, but the web app intentionally presents those on one /docs/cli page.
func docsURLForTopic(topic string) string {
	if topic == "" {
		return docsSiteURL
	}
	safe := sanitizeSlugForURL(topic)
	if _, ok := docsPageSlugs[safe]; ok {
		return docsSiteURL + "/" + safe
	}
	return cliDocsURL
}

// normalizeDocsURL repairs legacy server-emitted docs links at the CLI
// presentation boundary. The API still has older Problem.DocsURL values from
// docs.gregale.dev; preserving those in terminal output would send users to a
// host that currently serves 404s. URLs on other hosts are left untouched.
func normalizeDocsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != wire.DocsHost {
		return raw
	}
	switch {
	case u.Path == "" || u.Path == "/":
		return docsSiteURL
	case u.Path == "/storage":
		return storageDocsURL
	case strings.HasPrefix(u.Path, "/build/"):
		return deployFromSourceDocsURL
	default:
		return cliDocsURL
	}
}

// normalizeAPIBase keeps the CLI tolerant of the common shell form
// `FAAS_API=api.example.com`, while preserving explicit local/dev schemes.
// The SDK receives a URL prefix without trailing slashes.
func normalizeAPIBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultAPIBase
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}
