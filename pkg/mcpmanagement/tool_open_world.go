package mcpmanagement

// openWorldTools names the tools that reach outside the account's bounded
// Gregale workspace: public DNS and TLS for any domain, delivery to an
// external notification destination, or customer tools and jobs that may
// have external side effects. OpenAI's plugin review requires an explicit
// openWorldHint on every tool; everything else works inside the account.
var openWorldTools = map[string]bool{
	"gregale_check_domain":              true,
	"gregale_diagnose_domain":           true,
	"gregale_invoke_tool":               true,
	"gregale_start_job":                 true,
	"gregale_test_notification_channel": true,
	"gregale_test_tool":                 true,
}

// toolOpenWorld is the explicit openWorldHint for a tool.
func toolOpenWorld(name string) *bool {
	open := openWorldTools[name]
	return &open
}
