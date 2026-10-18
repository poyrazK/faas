package views

// SyntheticCheckView is one row of the app page's "Synthetic checks" table
// (ADR-748). Fields are pre-formatted at the handler edge.
type SyntheticCheckView struct {
	Name       string
	Request    string // "GET https://shop.gregale.dev/healthz"
	Every      string // "5 min"
	Uptime24h  string // "99.65%" or "—"
	P95        string // "840 ms" or "—"
	LastResult string // "ok · 200 · 92 ms" or "FAIL timeout"
	LastClass  string // "ok" | "bad" | "dim"
	Paused     bool
}
