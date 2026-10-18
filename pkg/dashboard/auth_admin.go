package dashboard

// AuthAdminData backs the application Auth page (ADR-1190). Its script reads
// AuthOrigin's public /settings/v1, and calls AuthOrigin's /admin/v1 from the
// owner's browser with a key the owner holds; the dashboard never receives
// the key or any user record. AuthOrigin is empty when the page cannot be
// used. Settings forms post to apid with CSRFToken; Changed and Error report
// the last change.
type AuthAdminData struct {
	Slug, AuthOrigin, Message string
	CSRFToken, Changed, Error string
	// RestartPending reports a change saved while the app was already
	// restarting, which may not have loaded it.
	RestartPending bool
	Providers      []string
	// Keys are the sealed signing keys; the script joins them with the
	// running app's published keys to show each key's state. MaxKeys is
	// api.AuthMaxSigningKeys.
	Keys    []AuthKey
	MaxKeys int
	// EmailProviders are the HTTPS email services the Auth app sends
	// through; SMSProviders the services it texts sign-in codes through.
	EmailProviders, SMSProviders []string
}

// AuthKey is a sealed signing key: Signer reports an AUTH_JWT_SIGNER_<ID>
// marker, which makes it the signer once the app restarts.
type AuthKey struct {
	ID, CreatedAt string
	Signer        bool
}
