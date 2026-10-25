package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

type menuAction struct {
	label    string
	args     []string
	run      func([]string) int
	needsApp bool
}

// Read only the menu answer. Buffered read-ahead must not consume input that
// belongs to the next command, especially a hidden secret value.
type menuInput struct{ io.Reader }

func (r menuInput) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

// menuActions lists the launcher's choices. Enter selects the first, so it
// is the guided start that a new user needs; Exit is last.
func menuActions() []menuAction {
	return []menuAction{
		{label: "Get an app live with guided setup", args: []string{"start"}, run: cmdStart},
		{label: "Inspect an app's health and deployment", args: []string{"inspect"}, run: cmdInspect},
		{label: "View app logs", args: []string{"logs"}, run: cmdLogs},
		{label: "Add or update app secrets", args: []string{"secrets", "set", "--interactive"}, run: cmdSecrets, needsApp: true},
		{label: "Remove app secrets", args: []string{"secrets", "unset", "--interactive"}, run: cmdSecrets, needsApp: true},
		{label: "Inspect an issue", args: []string{"issues", "get", "--interactive"}, run: cmdIssues},
		{label: "Run a Job", args: []string{"jobs", "run", "--interactive"}, run: cmdJobs},
		{label: "Inspect a workflow run and its steps", args: []string{"workflows", "status", "--interactive"}, run: cmdWorkflows},
		{label: "Exit"},
	}
}

func chooseMenuAction(actions []menuAction) (int, error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	choices := make([]string, len(actions))
	for i, action := range actions {
		choices[i] = action.label
	}
	prompt := startPrompt{reader: bufio.NewReader(menuInput{osStdin}), writer: osStdout}
	return prompt.choose(ctx, "What would you like to do?", choices, 0)
}

func cmdMenu(args []string) int {
	if len(args) != 0 {
		PrintUsage(osStderr, "usage: gregale menu", "menu")
		return 1
	}
	// json_flag.go: this launcher is terminal-only; child commands retain
	// their own machine-readable forms for automation.
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("gregale menu requires terminal input and output; use gregale help to choose a command for scripts"))
	}
	actions := menuActions()
	index, err := chooseMenuAction(actions)
	if err != nil {
		return startInputExit(err)
	}
	action := actions[index]
	if action.run == nil {
		return 0
	}
	if action.needsApp {
		slug, err := resolveReadAppTarget("")
		if err != nil {
			return readAppTargetError(err)
		}
		action.args = append(action.args, "--app", slug)
	}
	command := "gregale "
	if profileOverride != "" {
		command += "--profile " + strconv.Quote(profileOverride) + " "
	}
	command += strings.Join(action.args, " ")
	_, _ = fmt.Fprintf(osStdout, "\nOpening: %s\n\n", command)
	previousPath := invokedCommandPath
	invokedCommandPath = publicCommandPath(action.args)
	defer func() { invokedCommandPath = previousPath }()
	return action.run(action.args[1:])
}
