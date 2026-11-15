package main

import (
	"fmt"
	"strings"
)

// movedOperatorCommands maps operator commands that used to ship in gregale
// to their gregalectl replacement. Their routes accept only an operator
// session (or the FAAS_ADMIN_EMAILS allowlist), so no customer could run
// them; the notice keeps an operator's old habit from ending in "unknown
// command".
var movedOperatorCommands = []struct{ old, replacement string }{
	{"admin credit", "billing credit"},
	{"admin refund", "billing refund"},
	{"admin consume-credits", "billing consume-credits"},
	{"admin abuse-hold", "accounts abuse-hold"},
	{"admin egress-flows", "accounts egress-flows"},
	{"admin github-webhook-secret", "github webhook-secret"},
	{"admin postgres diagnostics", "billing postgres diagnostics"},
	{"admin postgres usage-import", "billing postgres usage-import"},
	{"admin postgres reconcile", "billing postgres reconcile"},
	{"github-webhook-secret", "github webhook-secret"},
	{"postgres diagnostics", "billing postgres diagnostics"},
	{"postgres usage-import", "billing postgres usage-import"},
	{"postgres reconcile", "billing postgres reconcile"},
	{"billing price-catalog", "billing price-catalog"},
	{"billing reconcile", "billing reconcile"},
	{"billing reconcile-paddle-overage", "billing reconcile-paddle-overage"},
	{"billing webhook-test", "billing webhook-test"},
}

// movedOperatorCommand prints where a moved operator command lives now.
func movedOperatorCommand(args []string) (int, bool) {
	for _, moved := range movedOperatorCommands {
		words := strings.Fields(moved.old)
		if len(args) < len(words) || strings.Join(args[:len(words)], " ") != moved.old {
			continue
		}
		return printErr("Operator command moved",
			fmt.Errorf("`gregale %s` moved to the operator CLI; run `gregalectl %s --help`", moved.old, moved.replacement)), true
	}
	return 0, false
}
