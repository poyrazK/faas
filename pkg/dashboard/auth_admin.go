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
	Providers                 []string
}
