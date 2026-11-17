package main

// cliArgumentCount describes the parser's explicit positional contract. Zero
// counts are meaningful; none of these fields contain supplied arguments.
type cliArgumentCount struct {
	Labels        []string `json:"labels"`
	ExpectedMin   int      `json:"expected_min"`
	ExpectedMax   int      `json:"expected_max"`
	ReceivedCount int      `json:"received_count"`
}

// rejectArgumentCount runs after flag parsing and before authentication or
// work. The caller supplies the actual parser rule and its existing usage.
func rejectArgumentCount(name, usage, topic string, received, minimum, maximum int, labels ...string) bool {
	if received >= minimum && received <= maximum {
		return false
	}
	var advice *cliFlagDiagnostic
	if jsonOutput && !jsonUsageHelp {
		advice = argumentCountDiagnostic(name, received, minimum, maximum, labels)
	}
	printUsageWithCLI(osStderr, usage, topic, advice)
	return true
}

func argumentCountDiagnostic(name string, received, minimum, maximum int, labels []string) *cliFlagDiagnostic {
	kind := "unexpected_argument"
	if received < minimum {
		kind = "missing_argument"
	}
	return &cliFlagDiagnostic{
		Kind: kind,
		Arguments: &cliArgumentCount{
			Labels: append([]string{}, labels...), ExpectedMin: minimum,
			ExpectedMax: maximum, ReceivedCount: received,
		},
		HelpArgv: flagDiagnosticHelpArgv(name, name),
	}
}
