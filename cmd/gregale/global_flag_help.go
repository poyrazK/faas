package main

import (
	"fmt"
	"strings"
)

const (
	globalFlagBeforeCommand   = "before_command"
	globalFlagBeforeSeparator = "before_separator"
)

// These are discovery contracts, not parsers. Descriptions retain the human
// root help's line breaks; JSON joins them without reading invocation state.
type globalFlagHelpDefinition struct {
	name, value, placement string
	spellings              []string
	description            []string
}

type jsonHelpGlobalFlag struct {
	Name        string   `json:"name"`
	Spellings   []string `json:"spellings"`
	Description string   `json:"description"`
	TakesValue  bool     `json:"takes_value"`
	Value       string   `json:"value"`
	Placement   string   `json:"placement"`
}

func globalFlagHelpDefinitions() []globalFlagHelpDefinition {
	return []globalFlagHelpDefinition{
		{name: "http-timeout", value: "DURATION", placement: globalFlagBeforeCommand, spellings: []string{"--http-timeout"},
			description: []string{"API request timeout, such as 90s (before the command)."}},
		{name: "debug-http", placement: globalFlagBeforeCommand, spellings: []string{"--debug-http"},
			description: []string{"Write HTTP metadata diagnostics to stderr (before the command)."}},
		{name: "agent-reason", value: "TEXT", placement: globalFlagBeforeCommand, spellings: []string{"--agent-reason"},
			description: []string{"With an agent key, tell the person who approves a request", "what it is for (before the command; up to 280 characters).", "Equivalent env: GREGALE_AGENT_REASON."}},
		{name: "json", placement: globalFlagBeforeSeparator, spellings: []string{"--json", "-j"},
			description: []string{"Machine-readable output where supported (NDJSON lists, RFC 7807", "errors on stderr; prompts stay human). Equivalent env: FAAS_JSON=1."}},
		{name: "non-interactive", placement: globalFlagBeforeCommand, spellings: []string{"--non-interactive"},
			description: []string{"Disable prompts and browser launches (before the command)."}},
		{name: "http-retries", value: "N", placement: globalFlagBeforeCommand, spellings: []string{"--http-retries"},
			description: []string{"Retry selected read-only API requests up to N times (0–5; before the command)."}},
		{name: "org", value: "SLUG", placement: globalFlagBeforeCommand, spellings: []string{"--org"},
			description: []string{"Act for an organization (before the command): projects,", "new apps, managed Postgres, buckets, plan and billing,", "e.g. gregale --org acme billing plan pro."}},
		{name: "profile", value: "NAME", placement: globalFlagBeforeCommand, spellings: []string{"--profile"},
			description: []string{"Select a connection profile (before the command)."}},
		{name: "wait-for-approval", placement: globalFlagBeforeCommand, spellings: []string{"--wait-for-approval"},
			description: []string{"When an agent key's request needs a person's approval,", "wait for the decision and retry once (before the", "command). Equivalent env: GREGALE_WAIT_FOR_APPROVAL=1."}},
	}
}

func jsonGlobalFlagHelp() []jsonHelpGlobalFlag {
	flags := []jsonHelpGlobalFlag{}
	for _, definition := range globalFlagHelpDefinitions() {
		flags = append(flags, jsonHelpGlobalFlag{
			Name: definition.name, Spellings: append([]string{}, definition.spellings...),
			Description: strings.Join(definition.description, " "),
			TakesValue:  definition.value != "", Value: definition.value, Placement: definition.placement,
		})
	}
	return flags
}

func writeGlobalFlagHelp(b *strings.Builder) {
	b.WriteString("\nGlobal flags:\n")
	for _, definition := range globalFlagHelpDefinitions() {
		label := definition.spellings[0]
		if definition.value != "" {
			label += " " + definition.value
		}
		fmt.Fprintf(b, "  %s%s%s\n", label, strings.Repeat(" ", max(2, 23-len(label))), definition.description[0])
		for _, line := range definition.description[1:] {
			fmt.Fprintf(b, "                         %s\n", line)
		}
	}
}
