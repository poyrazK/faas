package main_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// docCommand is one gregale command a customer page writes in code.
type docCommand struct {
	where string   // path:line
	text  string   // the command as written
	args  []string // the words after gregale, then its flags and values
}

// docCommands returns the gregale commands in a page's code spans and code
// blocks. A URL (`claude mcp add gregale https://…`) names the MCP server,
// not a command.
func docCommands(path, text string) []docCommand {
	var out []docCommand
	inBlock := false
	for n, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inBlock = !inBlock
			continue
		}
		codes := []string{line}
		if inBlock {
			if i := strings.Index(" "+line, " #"); i >= 0 {
				codes[0] = line[:i] // a shell comment
			}
		} else {
			codes = codes[:0]
			parts := strings.Split(line, "`")
			for i := 1; i < len(parts); i += 2 {
				codes = append(codes, parts[i])
			}
		}
		for _, code := range codes {
			for _, loc := range cliTreeCommandWords.FindAllStringSubmatchIndex(code, -1) {
				rest := code[loc[3]:]
				if strings.HasPrefix(rest, "://") {
					continue
				}
				region := cliTreeCommandFlags.FindString(rest)
				if i := strings.Index(region, " #"); i >= 0 {
					region = region[:i] // a shell comment
				}
				if region != "" && region[0] != ' ' {
					region = "" // text glued to the words: `"gregale dev",`
				}
				out = append(out, docCommand{
					where: fmt.Sprintf("%s:%d", path, n+1),
					text:  strings.TrimSpace(code[loc[0]:loc[3]] + region),
					args:  append(strings.Fields(code[loc[2]:loc[3]]), strings.Fields(region)...),
				})
			}
		}
	}
	return out
}

// docsFlagsNotInManifest lists the flags customer pages use that the CLI
// accepts but its manifest leaves out, so `--help`, the CLI reference and
// completion do not show them. The list only shrinks: add the flag to the
// command's manifest entry (cli_meta.go) and delete its line.
const docsFlagsNotInManifest = "testdata/docs_flags_not_in_manifest.txt"

// Every gregale command the customer docs, starter templates and agent
// guides write must run as written: the command and verbs exist and the
// command takes each flag shown. Agents copy commands literally, so a
// renamed verb or a removed flag in a page is a failed run.
func TestDocsCommandsRunAsWritten(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "docs") // the realtime pages document the preview's verbs
	// A probe that runs before its flags parse fails against this API
	// instead of reaching a real one.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "docs command probes have no API", http.StatusServiceUnavailable)
	}))
	defer api.Close()
	t.Setenv("FAAS_API", api.URL)
	t.Setenv("FAAS_TOKEN", "")
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // no linked checkout may resolve an app
	if v := probe(t, []string{"crons", "list", "--no-such-flag-for-the-probe"}); v.rejected == "" {
		t.Fatalf("the probe accepts a flag no command takes: %+v", v)
	}
	if v := probe(t, []string{"crons", "list", "--app", "my-api"}); !v.parsed {
		t.Fatalf("the probe rejects a flag the command takes: %+v", v)
	}

	unlisted := map[string]string{} // "path --flag" -> first page that uses it
	verdicts := map[string]probeVerdict{}
	checked, undecided := 0, 0
	for _, path := range docCommandPages(t, dir) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(filepath.Join(dir, "../.."), path)
		for _, cmd := range docCommands(rel, string(raw)) {
			checked++
			check := main.CheckDocCommand(cmd.args)
			if check.Problem == "" && len(check.Unlisted) == 0 {
				continue // the manifest knows the command and every flag
			}
			key := strings.Join(check.Args, "\x00")
			verdict, seen := verdicts[key]
			if !seen {
				verdict = probe(t, check.Args)
				verdicts[key] = verdict
			}
			switch {
			case verdict.rejected != "":
				t.Errorf("%s: `%s`: %s", cmd.where, cmd.text, verdict.rejected)
			case !verdict.parsed:
				undecided++ // the command answered before its flags parsed
			case check.Problem != "":
				unlisted[strings.Join(check.Path, " ")+" "+check.Args[len(check.Path)]+" (word not in the manifest)"] = cmd.where
			default:
				for _, flag := range check.Unlisted {
					gap := strings.Join(check.Path, " ") + " --" + flag
					if _, ok := unlisted[gap]; !ok {
						unlisted[gap] = cmd.where
					}
				}
			}
		}
	}
	t.Logf("checked %d commands; %d probes answered before parsing flags", checked, undecided)
	checkManifestGaps(t, dir, unlisted)
}

// docCommandPages are the docs site's pages and the other customer pages
// that spell commands (cliTreeDocs), without the generated references.
func docCommandPages(t *testing.T, dir string) []string {
	t.Helper()
	paths := customerDocPages(t, dir)
	for _, pattern := range cliTreeDocs {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	return slices.DeleteFunc(paths, func(path string) bool {
		return slices.ContainsFunc(cliTreeDocsGenerated, func(generated string) bool { return path == filepath.Join(dir, generated) })
	})
}

// probeStop is a flag no command takes. A probe writes it after the flag it
// asks about, so the parser fails on one of the two before the command runs.
const probeStop = "zz-docs-probe-stop"

// probeVerdict is what the command's own parser said about a line.
type probeVerdict struct {
	parsed   bool   // every flag parsed: the parser reached probeStop
	rejected string // why the CLI refused the line: an unknown flag or verb
}

// probe runs args with probeStop after them. The parser fails on the first
// flag it does not take, so the command never runs: a failure on probeStop
// means the whole line parsed. Parse errors go to the process's stderr.
func probe(t *testing.T, args []string) probeVerdict {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = write
	_, _, cliErr := runCLI(t, append(append([]string{"--non-interactive"}, args...), "--"+probeStop)...)
	os.Stderr = stderr
	_ = write.Close()
	raw, _ := io.ReadAll(read)
	_ = read.Close()
	out := string(raw) + cliErr
	if strings.Contains(out, "unknown flag --"+probeStop) || strings.Contains(out, "flag provided but not defined: -"+probeStop) {
		return probeVerdict{parsed: true}
	}
	if m := unknownFlagLine.FindStringSubmatch(out); m != nil {
		return probeVerdict{rejected: "the command takes no --" + m[1] + m[2]}
	}
	if m := unknownCommandLine.FindString(out); m != "" {
		return probeVerdict{rejected: strings.TrimSpace(m)}
	}
	return probeVerdict{}
}

// unknownFlagLine and unknownCommandLine are the parser's refusals.
var (
	unknownFlagLine    = regexp.MustCompile(`unknown flag --([a-z0-9][a-z0-9-]*)|flag provided but not defined: -+([a-z0-9][a-z0-9-]*)`)
	unknownCommandLine = regexp.MustCompile(`(?m)^.*unknown (?:[a-z-]+ )?(?:sub)?command "[^"\n]*".*$`)
)

// checkManifestGaps holds the gaps to the baseline: a new one fails, and so
// does a listed one the docs no longer show (delete its line).
func checkManifestGaps(t *testing.T, dir string, found map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, docsFlagsNotInManifest))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			listed[line] = true
		}
	}
	if os.Getenv("GREGALE_UPDATE_DOCS_FLAG_GAPS") == "1" {
		keys := make([]string, 0, len(found))
		for key := range found {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		body := "# Flags customer pages use that the CLI accepts but its manifest (cli_meta.go)\n" +
			"# leaves out of --help, the CLI reference and completion. Only shrinks:\n" +
			"# add the flag to the manifest and delete its line (TestDocsCommandsRunAsWritten).\n" +
			strings.Join(keys, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, docsFlagsNotInManifest), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for key, where := range found {
		if !listed[key] {
			t.Errorf("%s: gregale %s is not in the CLI manifest, so --help and the CLI reference leave it out; add it to the command in cli_meta.go", where, key)
		}
	}
	for key := range listed {
		if _, ok := found[key]; !ok {
			t.Errorf("%s lists gregale %s, which no page uses unlisted any more; delete the line", docsFlagsNotInManifest, key)
		}
	}
}

func TestDocCommandsReadsCodeOnly(t *testing.T) {
	page := "Run `gregale cron list --app my-api` daily; gregale in prose is not code.\n" +
		"```bash\n" +
		"gregale deploy --wait   # deploys and waits\n" +
		"# Authenticate gregale for the account.\n" +
		"claude mcp add --transport http gregale https://api.gregale.dev/mcp\n" +
		"```\n" +
		"```json\n" +
		"{\"command\": \"gregale dev\",}\n" +
		"```\n"
	var got []string
	for _, cmd := range docCommands("p.md", page) {
		got = append(got, cmd.where+" "+strings.Join(cmd.args, " "))
	}
	want := []string{"p.md:1 cron list --app my-api", "p.md:3 deploy --wait", "p.md:8 dev"}
	if !slices.Equal(got, want) {
		t.Fatalf("docCommands = %q, want %q", got, want)
	}
}
