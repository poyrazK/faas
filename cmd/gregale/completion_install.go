package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

const completionInstallMarker = "# Gregale managed completion script\n"
const completionBlockStart = "# >>> gregale completion >>>"
const completionBlockEnd = "# <<< gregale completion <<<"

func cmdCompletionInstall(args []string) int {
	fs := newFlagSet("completion install", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose shell and review installation paths")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 {
		return printErr("Invalid completion install flags", errors.New("use completion install --interactive"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use completion bash, zsh, fish, or powershell to generate a script for manual setup"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return printErr("Could not find home directory", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	shells := []string{"bash", "zsh", "fish", "powershell"}
	detected := strings.ToLower(filepath.Base(os.Getenv("SHELL")))
	fallback := 0
	if runtime.GOOS == "windows" {
		fallback = 3
	}
	for i, shell := range shells {
		if detected == shell || shell == "powershell" && detected == "pwsh" {
			fallback = i
		}
	}
	choice, err := prompt.choose(ctx, "Choose your shell (default inferred from SHELL or platform).", shells, fallback)
	if err != nil {
		return startInputExit(err)
	}
	shell := shells[choice]
	config, err := os.UserConfigDir()
	if err != nil {
		return printErr("Could not find configuration directory", err)
	}
	script := filepath.Join(config, "gregale", "completions", "gregale."+shell)
	startup := filepath.Join(home, ".bashrc")
	if runtime.GOOS == "darwin" {
		startup = filepath.Join(home, ".bash_profile")
	}
	if shell == "zsh" {
		zdir := os.Getenv("ZDOTDIR")
		if zdir == "" {
			zdir = home
		}
		startup = filepath.Join(zdir, ".zshrc")
		script = filepath.Join(config, "gregale", "completions", "zsh", "_gregale")
	}
	if shell == "fish" {
		fishConfig := os.Getenv("XDG_CONFIG_HOME")
		if fishConfig == "" {
			fishConfig = filepath.Join(home, ".config")
		}
		script = filepath.Join(fishConfig, "fish", "completions", "gregale.fish")
		startup = ""
	}
	if shell == "powershell" {
		script = filepath.Join(config, "gregale", "completions", "gregale.ps1")
		_, _ = fmt.Fprintln(prompt.writer, "In your PowerShell host, run $PROFILE and enter its full path here.")
		startup = ""
	}
	if shell != "fish" {
		startup, err = prompt.text(ctx, "Shell startup/profile file (absolute path)", startup)
		if err != nil {
			return startInputExit(err)
		}
		if !filepath.IsAbs(startup) || strings.IndexFunc(startup, unicode.IsControl) >= 0 {
			return printErr("Invalid startup path", errors.New("enter an absolute path without control characters"))
		}
	}
	if !filepath.IsAbs(script) || strings.IndexFunc(script, unicode.IsControl) >= 0 {
		return printErr("Invalid completion path", errors.New("configuration directory must be absolute without control characters"))
	}
	var rendered bytes.Buffer
	rendered.WriteString(completionInstallMarker)
	switch shell {
	case "bash":
		renderBashCompletion(&rendered)
	case "zsh":
		renderZshCompletion(&rendered)
	case "fish":
		renderFishCompletion(&rendered)
	case "powershell":
		renderPowershellCompletion(&rendered)
	}
	scriptOld, scriptMode, err := readCompletionInstallFile(script)
	if err != nil {
		return printErr("Could not inspect completion file", err)
	}
	if len(scriptOld) > 0 && !bytes.HasPrefix(scriptOld, []byte(completionInstallMarker)) {
		return printErr("Completion file already exists", errors.New("preserve the existing file and use manual completion setup"))
	}
	var startupOld, startupNew []byte
	startupMode := os.FileMode(0o600)
	if startup != "" {
		if target, err := filepath.EvalSymlinks(startup); err == nil {
			startup = target
		} else if !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not resolve startup file", err)
		}
		if filepath.Clean(startup) == filepath.Clean(script) {
			return printErr("Invalid startup path", errors.New("startup file and completion script must be different files"))
		}
		startupOld, startupMode, err = readCompletionInstallFile(startup)
		if err != nil {
			return printErr("Could not read startup file", err)
		}
		line := "source " + quoteLogCommandArg(script)
		if shell == "powershell" {
			line = ". '" + strings.ReplaceAll(script, "'", "''") + "'"
		}
		if shell == "zsh" {
			line = "fpath=(" + quoteLogCommandArg(filepath.Dir(script)) + " $fpath)\nautoload -Uz compinit\n(( $+functions[compdef] )) || compinit\nautoload -Uz _gregale\ncompdef _gregale gregale"
		}
		startupNew, err = completionStartupBlock(startupOld, line)
		if err != nil {
			return printErr("Could not update managed completion block", err)
		}
	}
	PrintProgress(osStdout, "Shell: %s\nCompletion script: %s", shell, script)
	if startup != "" {
		PrintProgress(osStdout, "Startup file: %s\nExisting startup content will be preserved; changed content is backed up before saving.", startup)
	}
	if bytes.Equal(scriptOld, rendered.Bytes()) && (startup == "" || bytes.Equal(startupOld, startupNew)) {
		PrintOK(osStdout, "Completion is already installed. Open a new %s session to activate it.", shell)
		return 0
	}
	confirmed, err := prompt.confirm(ctx, "Install Gregale completion at these paths?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Completion installation canceled.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	if err := checkCompletionInstallFile(script, scriptOld); err != nil {
		return printErr("Completion file changed", err)
	}
	if startup != "" {
		if err := checkCompletionInstallFile(startup, startupOld); err != nil {
			return printErr("Startup file changed", err)
		}
	}
	if err := writeCompletionInstallFile(script, rendered.Bytes(), scriptMode); err != nil {
		return printErr("Could not save completion script", err)
	}
	if startup != "" && !bytes.Equal(startupOld, startupNew) {
		if len(startupOld) > 0 {
			backup, err := os.CreateTemp(filepath.Dir(startup), filepath.Base(startup)+".gregale-backup-*")
			if err != nil {
				return printErr("Script saved, but startup backup failed", err)
			}
			_, writeErr := backup.Write(startupOld)
			closeErr := backup.Close()
			if writeErr != nil {
				return printErr("Script saved, but startup backup failed", writeErr)
			}
			if closeErr != nil {
				return printErr("Script saved, but startup backup failed", closeErr)
			}
			PrintProgress(osStdout, "Startup backup: %s", backup.Name())
		}
		if err := writeCompletionInstallFile(startup, startupNew, startupMode); err != nil {
			return printErr("Script saved, but startup update failed", err)
		}
	}
	PrintOK(osStdout, "Completion installed. Open a new %s session to activate it; gregale must be on PATH.", shell)
	return 0
}

func completionStartupBlock(old []byte, line string) ([]byte, error) {
	if !utf8.Valid(old) || bytes.IndexByte(old, 0) >= 0 {
		return nil, errors.New("startup file must be UTF-8 text; use manual setup for other encodings")
	}
	text := string(old)
	block := completionBlockStart + "\n" + line + "\n" + completionBlockEnd
	starts, ends := strings.Count(text, completionBlockStart), strings.Count(text, completionBlockEnd)
	if starts == 0 && ends == 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return []byte(text + block + "\n"), nil
	}
	start, end := strings.Index(text, completionBlockStart), strings.Index(text, completionBlockEnd)
	if starts != 1 || ends != 1 || end < start || start > 0 && text[start-1] != '\n' {
		return nil, errors.New("managed completion markers are malformed or duplicated; repair them before installing")
	}
	startEnd := start + len(completionBlockStart)
	if startEnd < len(text) && text[startEnd] != '\n' && text[startEnd] != '\r' {
		return nil, errors.New("managed completion start marker must be on its own line")
	}
	if end > 0 && text[end-1] != '\n' {
		return nil, errors.New("managed completion end marker must be on its own line")
	}
	end += len(completionBlockEnd)
	if end < len(text) && text[end] != '\n' && text[end] != '\r' {
		return nil, errors.New("managed completion end marker must be on its own line")
	}
	return []byte(text[:start] + block + text[end:]), nil
}

func readCompletionInstallFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0o600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("target must be a regular file")
	}
	if info.Size() > 4<<20 {
		return nil, 0, errors.New("target exceeds 4 MiB")
	}
	body, err := os.ReadFile(path)
	return body, info.Mode().Perm(), err
}

func checkCompletionInstallFile(path string, old []byte) error {
	current, _, err := readCompletionInstallFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, old) {
		return errors.New("file changed while reviewing; run installation again")
	}
	return nil
}

func writeCompletionInstallFile(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gregale-completion-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
