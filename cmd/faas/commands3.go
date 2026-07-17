// commands3.go — `faas secrets` subcommand (spec §11/G2).
//
// `faas secrets {list,set,unset} --app <slug>` is the customer surface for
// sealed-at-rest env injection. The CLI transports plaintext values only
// over TLS to apid; the seal happens server-side and the ciphertext never
// re-enters the CLI.
//
// Operations:
//   faas secrets list   --app <slug>
//   faas secrets set    --app <slug> KEY=VALUE [--from-stdin]
//   faas secrets unset  --app <slug> KEY
//
// `--from-stdin` reads the value from stdin (one pair per line, KEY=VALUE)
// for pipelines that need to avoid putting the plaintext in shell
// history. Most usage is the inline form.

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// osStdout and osStdin are the package-level I/O seams so tests can pipe
// data in (--from-stdin) and capture output (success messages) without
// spawning a subprocess. Production wiring points them at the real
// os.Stdout / os.Stdin.
var (
	osStdout io.Writer = os.Stdout
	osStdin  io.Reader = os.Stdin
)

func cmdSecrets(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: faas secrets <list|set|unset> --app <slug> [args]")
		return 1
	}
	switch args[0] {
	case "list":
		return secretsList(args[1:])
	case "set":
		return secretsSet(args[1:])
	case "unset":
		return secretsUnset(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown secrets subcommand %q\n", args[0])
	return 1
}

// --- list ------------------------------------------------------------------

func secretsList(args []string) int {
	fs := flag.NewFlagSet("secrets list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		fmt.Fprintln(os.Stderr, "usage: faas secrets list --app <slug>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListSecrets(context.Background(), *app)
	if err != nil {
		return printErr("List failed", err)
	}
	if resp.Count == 0 {
		fmt.Fprintf(osStdout, "%s: no secrets (0/%d)\n", *app, resp.Quota)
		return 0
	}
	fmt.Fprintf(osStdout, "%s: %d/%d secrets\n", *app, resp.Count, resp.Quota)
	for _, s := range resp.Secrets {
		fmt.Fprintf(osStdout, "  %s\n", s.Key)
	}
	return 0
}

// --- set -------------------------------------------------------------------

func secretsSet(args []string) int {
	fs := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE pairs from stdin (one per line)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		fmt.Fprintln(os.Stderr, "usage: faas secrets set --app <slug> KEY=VALUE [...] [--from-stdin]")
		return 1
	}

	pairs := []secretsPair{}
	if *fromStdin {
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "secret set: --from-stdin takes no positional pairs")
			return 1
		}
		scanner := bufio.NewScanner(osStdin)
		// A 64 KB line cap is enough for SecretValueMaxBytes at Scale (32 KB)
		// plus the key name. Larger lines silently truncate today; the
		// apid-side byte cap will still reject the request.
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p, err := parseSecretsPair(line)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			pairs = append(pairs, p)
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			fmt.Fprintln(os.Stderr, "read stdin:", err)
			return 1
		}
	} else {
		for _, a := range fs.Args() {
			p, err := parseSecretsPair(a)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			pairs = append(pairs, p)
		}
	}
	if len(pairs) == 0 {
		fmt.Fprintln(os.Stderr, "secret set: at least one KEY=VALUE pair is required")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	for _, p := range pairs {
		if err := client.SetSecret(context.Background(), *app, p.Key, p.Value); err != nil {
			return printErr("Set "+p.Key+" failed", err)
		}
		fmt.Fprintf(osStdout, "✓ %s set\n", p.Key)
	}
	return 0
}

type secretsPair struct {
	Key   string
	Value string
}

// parseSecretsPair splits KEY=VALUE. The first '=' is the split point, so
// values may contain more '=' (e.g. base64 'A=B=C'). Empty KEY is rejected.
func parseSecretsPair(s string) (secretsPair, error) {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return secretsPair{}, fmt.Errorf("secret set: %q must look like KEY=VALUE", s)
	}
	key := s[:i]
	value := s[i+1:]
	if key == "" {
		return secretsPair{}, fmt.Errorf("secret set: empty KEY in %q", s)
	}
	return secretsPair{Key: key, Value: value}, nil
}

// --- unset -----------------------------------------------------------------

func secretsUnset(args []string) int {
	fs := flag.NewFlagSet("secrets unset", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: faas secrets unset --app <slug> KEY")
		return 1
	}
	key := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.UnsetSecret(context.Background(), *app, key); err != nil {
		return printErr("Unset failed", err)
	}
	fmt.Fprintf(osStdout, "✓ %s unset\n", key)
	return 0
}
