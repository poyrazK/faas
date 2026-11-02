// adr: 20261023115741 — a starter template or a gregale.yaml that needs a
// capability the account does not have is left out of `gregale init --list`
// and refused before a deploy creates anything, instead of failing halfway
// with a gated "no such API route" (#5756 item 7).

package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/apid/capgate"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/productcap"
)

// templateRequiredSecrets are the secrets a template needs before its first
// start: they configure a service the customer brings (Upstash, Slack, a
// Postgres URL). Optional settings are not listed.
var templateRequiredSecrets = map[string][]string{
	"object-storage-python":    {"STORAGE_APP_TOKEN"},
	"object-storage-node":      {"STORAGE_APP_TOKEN"},
	"s3-uploader":              {"S3_BUCKET", "S3_REGION", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"},
	"slack-bot":                {"SLACK_SIGNING_SECRET"},
	"rest-api-postgres":        {"DATABASE_URL"},
	"rest-api-postgres-python": {"DATABASE_URL"},
	"secret-reload-node":       {"DATABASE_URL"},
	"customer-platform":        {"DATABASE_URL"},
	"cron-worker":              {"QSTASH_CURRENT_SIGNING_KEY", "QSTASH_NEXT_SIGNING_KEY", "UPSTASH_REDIS_REST_URL", "UPSTASH_REDIS_REST_TOKEN"},
}

// manifestCapabilityIndex maps the catalog's manifest settings to the
// capability that owns each, as apid's capability gate does.
type manifestCapabilityIndex struct {
	bySetting map[string]string
	names     map[string]string
}

var manifestCapabilities = sync.OnceValue(func() manifestCapabilityIndex {
	index := manifestCapabilityIndex{bySetting: map[string]string{}, names: map[string]string{}}
	catalog, err := productcap.Load()
	if err != nil {
		return index
	}
	for _, capability := range catalog.Capabilities {
		index.names[capability.ID] = capability.Name
		for _, setting := range capability.Settings {
			index.bySetting[setting] = capability.ID
		}
	}
	return index
})

// capabilitiesFor returns the catalog capabilities a manifest turns on, in
// manifest order. A value-qualified setting falls back to its field, as
// capgate.Gate.SettingCapability does.
func capabilitiesFor(m *gregalemanifest.Manifest) []string {
	index := manifestCapabilities()
	var out []string
	for _, setting := range capgate.ManifestSettings(m) {
		id, ok := index.bySetting[setting]
		if !ok {
			if field, _, qualified := strings.Cut(setting, "="); qualified {
				id, ok = index.bySetting[field]
			}
		}
		if ok && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func capabilityDisplayName(id string) string {
	if name := manifestCapabilities().names[id]; name != "" {
		return name
	}
	return id
}

// templateManifest is a template's parsed gregale.yaml, or nil.
func templateManifest(name string) *gregalemanifest.Manifest {
	raw, err := fs.ReadFile(templates.FS, name+"/gregale.yaml")
	if err != nil {
		return nil
	}
	m, err := gregalemanifest.ParseBytes(raw)
	if err != nil {
		return nil
	}
	return m
}

// sourceManifest is a source directory's parsed gregale.yaml, or nil when it
// has none or it cannot be read; the deploy reports an unreadable manifest
// itself.
func sourceManifest(dir string) *gregalemanifest.Manifest {
	m, ok, err := gregalemanifest.Load(dir)
	if err != nil || !ok {
		return nil
	}
	return m
}

// templateShown reports whether the view shows every capability a template
// needs, as GET /v1/templates decides for the dashboard.
func templateShown(view capabilityView, name string) bool {
	for _, capability := range capabilitiesFor(templateManifest(name)) {
		if view.hides(capability) {
			return false
		}
	}
	return true
}

// templateNeedsNote names what a listed template needs beyond the account:
// the secrets of a service the customer brings.
func templateNeedsNote(name string) string {
	if name == "ai-chat" {
		return "needs OPENAI_API_KEY or ANTHROPIC_API_KEY"
	}
	if secrets := templateRequiredSecrets[name]; len(secrets) > 0 {
		return "needs " + strings.Join(secrets, ", ")
	}
	return ""
}

// refuseHiddenManifestCapabilities refuses a deploy whose template or
// gregale.yaml turns on a capability the account does not have, before any
// app or upload exists. apid refuses those settings too, but a queue binding
// is reconciled through its own routes after the deploy, which answered a
// bare 404 once the app and release already existed.
func refuseHiddenManifestCapabilities(ctx context.Context, template, sourceDir string) error {
	var m *gregalemanifest.Manifest
	what := "gregale.yaml"
	switch {
	case template != "":
		m, what = templateManifest(template), "template "+template
	case sourceDir != "":
		m = sourceManifest(sourceDir)
	}
	for _, capability := range capabilitiesFor(m) {
		if deployCapabilityHidden(ctx, capability) {
			return fmt.Errorf("%s uses %s, which is not enabled for this account, so nothing was deployed; see `gregale feature list --early-access`", what, capabilityDisplayName(capability))
		}
	}
	return nil
}

// deployCapabilityHidden reports whether the server hides capability from the
// account. A cached view that releases it is trusted; otherwise, including a
// first run with no cache, the server is asked (once: the answer is cached).
// Without a token or an answer nothing is refused here; apid still refuses.
func deployCapabilityHidden(ctx context.Context, capability string) bool {
	if capabilityReleaseAll() {
		return false
	}
	token := loadTokenWithDiagnostics(io.Discard)
	if token == "" {
		return false
	}
	cached, _, haveCache := cachedHiddenCapabilities(token)
	if haveCache && !slices.Contains(cached, capability) {
		return false
	}
	if fresh, read := readHiddenCapabilitiesContext(ctx, token); read {
		return slices.Contains(fresh, capability)
	}
	return haveCache // the cache hid it and the server did not answer
}
