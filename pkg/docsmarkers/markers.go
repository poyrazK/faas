// Package docsmarkers finds operator and engineering material in text bound
// for a published customer docs page. cmd/docs-links-check refuses a page
// that contains any; cmd/errorcodes-md leaves such strings out of the page it
// generates.
package docsmarkers

import (
	"regexp"
	"strings"
)

// internalRE finds operator and engineering material on a customer page. Each
// alternative names something a customer cannot act on.
var internalRE = regexp.MustCompile(strings.Join([]string{
	`\bADRs?[- ]?\d`,
	`\]\((?:\.\./)*(?:adr|ops|runbooks|drills)/`,
	`\b(?:apid|schedd|vmmd|builderd|imaged|meterd|githubd|outboundd|pgproxyd|gatewayd(?:-public|-internal)?)\b`,
	`\bpkg/(?:api|apid|state|sched|fcvm|netns|oci|rootfs|meter|billing|wire|imaged|builderd|githubd|legal|secretbox|servicecaller)\b`,
	`\bmigrations/\d`,
	`faas_implementation_spec|\bspec ?§|spec-§`,
	`\bFAAS_[A-Z0-9_]+`,
	`/etc/faas/(?:secrets|[a-z-]+\.env)|journalctl|systemctl`,
	`native (?:qualification|acceptance|KVM)|qualified (?:server|installation)|qualification harness`,
	`operators? (?:can|must|may) (?:set|enable|configure)|ask (?:your|the) operator|Gregale operator`,
}, "|"))

// customerEnvRE matches the FAAS_ names customers do use: the CLI's settings
// and the variables Gregale sets inside an app.
var customerEnvRE = regexp.MustCompile(`^FAAS_(?:TOKEN|API|JSON|COMPLETION_CACHE_PATH|DEVELOPER_ID|DEVELOPER_ENVIRONMENT|RUNTIME|APP_ID|APP_SLUG|TENANT_ID|DEPLOYMENT_ID|DEPLOYMENT_TAG|DEPLOYMENT_CREATED_AT|INSTANCE_ID|INSTANCE_IDENTITY_FILE|NODE_ID|REGION|COMMIT_SHA|IMAGE_DIGEST|EVENT_PUBLISH_ENDPOINT|METADATA_ENV_ENDPOINT|SECRETS_[A-Z_]+|WORKLOAD_[A-Z0-9_]*)$`)

// Internal returns the internal markers in line, skipping the FAAS_ names
// customers use.
func Internal(line string) []string {
	var found []string
	for _, m := range internalRE.FindAllString(line, -1) {
		if !customerEnvRE.MatchString(m) {
			found = append(found, m)
		}
	}
	return found
}
