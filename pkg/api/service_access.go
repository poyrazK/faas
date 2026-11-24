package api

// Declared service access (ADR-20261124090000): who may call a project
// service's public URL. Set by gregale.yaml project.services.<name>.access or
// Compose x-gregale-access, and applied on every production deploy.
const (
	// ServiceAccessPublic opens the public URL to anyone: no Gregale API key
	// (require_authn off, and a bearer gate opens). Basic auth, an IP
	// allowlist and internal-only stay.
	ServiceAccessPublic = "public"
	// ServiceAccessKey requires a Gregale API key of the owning account.
	ServiceAccessKey = "key"
)
