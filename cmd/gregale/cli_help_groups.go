package main

// cliHelpGroups orders `gregale help --all` around the customer journey:
// getting started, building and shipping, running in production,
// configuration, async work and data, then the account. Commands keep their
// names; only discovery changes. Every customer command belongs to exactly
// one group (TestCliHelpGroupsCoverCustomerCommands). Operator and
// compatibility commands are listed by `gregale help --all --operator`.
var cliHelpGroups = []struct {
	Title    string
	Commands []string
}{
	{"Get started", []string{
		"start", "init", "signup", "login", "link", "unlink", "menu", "doctor", "open", "dashboard",
		"context", "config", "completion", "man", "version",
	}},
	{"Develop & deploy", []string{
		"deploy", "dev", "apps", "app", "build", "deploys", "deployment", "deployments", "rollback",
		"preview", "projects", "diff", "invoke", "test", "chaos", "scan", "registry", "github", "connect",
		"mcp", "openapi", "routes", "flags", "canary", "traffic", "mirror", "freeze", "release-gates",
		"approvals", "scheduled-releases", "upload-cache",
	}},
	{"Operate & observe", []string{
		"logs", "tail", "inspect", "debug", "trace", "metrics", "analytics", "status", "ps", "wake",
		"park", "wake-ahead", "wake-timeline", "throttle-suggestions", "issues", "alerts", "channels",
		"slo", "slos", "synthetics", "log-drains", "audit-events",
	}},
	{"Configure", []string{
		"domains", "env", "secrets", "cors", "edge-rules", "edge-rule-lists", "cache", "keys",
		"consumers", "platform-tenants", "trusted-publishers", "auth",
	}},
	{"Async & data", []string{
		"add", "bindings", "bucket", "ai", "events", "send", "deliver", "webhooks", "queue", "dlq",
		"crons", "triggers", "jobs", "workers", "workflows", "automations", "delayed-task", "run",
		"runs", "invocations", "operations", "customer-operations", "pipeline", "data-api", "realtime",
	}},
	{"Account & billing", []string{
		"whoami", "account", "orgs", "invitations", "mfa", "profile", "logout", "plan", "billing",
		"usage", "invoices", "overage-cap", "capabilities",
	}},
}

// cliHelpGroupOther holds customer commands missing from cliHelpGroups so
// they stay discoverable; the coverage test keeps it empty.
const cliHelpGroupOther = "Other"

// cliHelpGroup returns the root help section for a customer command, or ""
// for operator and compatibility commands.
func cliHelpGroup(command cliCommand) string {
	if command.Audience != cliAudienceCustomer {
		return ""
	}
	for _, group := range cliHelpGroups {
		for _, name := range group.Commands {
			if name == command.Name {
				return group.Title
			}
		}
	}
	return cliHelpGroupOther
}

// rootHelpFlags reports whether args only choose a root help view:
// --all lists every customer command and --operator adds operator commands.
func rootHelpFlags(args []string) (showAll, showOperator, ok bool) {
	for _, arg := range args {
		switch arg {
		case "--all":
			showAll = true
		case "--operator":
			showAll, showOperator = true, true
		case "--help", "-h":
		default:
			return false, false, false
		}
	}
	return showAll, showOperator, true
}

// cliRelatedCommands links command names that are easy to confuse. Command
// help, man pages and the reference list the rest of a command's set under
// "See also".
var cliRelatedCommands = [][]string{
	{"deploy", "deploys", "deployment", "deployments"},
	{"logs", "tail"},
	{"slo", "slos"},
	{"run", "runs"},
	{"crons", "triggers"},
	{"env", "secrets"},
	{"connect", "github"},
	{"plan", "billing", "usage", "invoices", "overage-cap"},
}

// cliSeeAlso returns the members of commands related to name, in set order.
func cliSeeAlso(name string, commands []cliCommand) []cliCommand {
	var related []cliCommand
	for _, set := range cliRelatedCommands {
		if !containsCompletionString(set, name) {
			continue
		}
		for _, other := range set {
			for _, command := range commands {
				if other != name && command.Name == other {
					related = append(related, command)
				}
			}
		}
	}
	return related
}
