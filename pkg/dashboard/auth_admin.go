package dashboard

// AuthAdminData backs the application Auth users and organizations page
// (ADR-1190). Its script calls AuthOrigin's /admin/v1 from the owner's
// browser with a key the owner holds; the dashboard never receives the key
// or any user record. AuthOrigin is empty when the page cannot be used.
type AuthAdminData struct {
	Slug, AuthOrigin, Message string
}
