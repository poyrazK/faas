package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Subcommand names — lifted to constants so goconst stops flagging the
// repeated "list"/"add"/"rm" string literals in the dispatch tables below.
const (
	subList = "list"
	subAdd  = "add"
	subRm   = "rm"

	statusPending = "pending"
	statusVerified = "verified"
)

// cmdApp implements `faas app <slug>` (GET /v1/apps/{slug}), `faas app <slug>
// --ram N`, and `faas apps -q <slug>` (DELETE) — UX §2.4.
//
// UpdateAppRequest uses *int pointers on the wire so callers can distinguish
// "unset" from "explicit zero." We use fs.Visit to detect which flags the
// user actually passed — comparing flag values to sentinels (0 / -1) would
// silently drop valid inputs like `--ram 0` or `--idle -1`.
func cmdApp(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: faas app <slug> [--ram N] [--max-concurrency N] [--idle SEC]")
		return 1
	}
	slug := args[0]
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	ram := fs.Int("ram", 0, "update RAM (MB)")
	conc := fs.Int("max-concurrency", 0, "update max concurrent requests")
	idle := fs.Int("idle", 0, "update idle timeout (seconds)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	// Build the partial-update payload from explicit flags only.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	var req api.UpdateAppRequest
	if explicit["ram"] {
		v := *ram
		req.RAMMB = &v
	}
	if explicit["max-concurrency"] {
		v := *conc
		req.MaxConcurrency = &v
	}
	if explicit["idle"] {
		v := *idle
		req.IdleTimeoutS = &v
	}

	if req.RAMMB == nil && req.MaxConcurrency == nil && req.IdleTimeoutS == nil {
		a, err := client.GetApp(ctx, slug)
		if err != nil {
			return printErr("Could not fetch app", err)
		}
		fmt.Printf("%-30s %s\n", "slug:", a.Slug)
		fmt.Printf("%-30s %s\n", "url:", a.URL)
		fmt.Printf("%-30s %d MB\n", "ram:", a.RAMMB)
		fmt.Printf("%-30s %d\n", "max concurrency:", a.MaxConcurrency)
		fmt.Printf("%-30s %ds\n", "idle timeout:", a.IdleTimeoutS)
		fmt.Printf("%-30s %s\n", "status:", a.Status)
		return 0
	}

	if _, err := client.UpdateApp(ctx, slug, req); err != nil {
		return printErr("Update failed", err)
	}
	fmt.Println("✓ Updated")
	return 0
}

// cmdAppsRm implements `faas apps -q <slug>` (DELETE /v1/apps/{slug}).
func cmdAppsRm(args []string) int {
	fs := flag.NewFlagSet("apps-rm", flag.ContinueOnError)
	quiet := fs.Bool("q", false, "suppress confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: faas apps -q <slug>")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "Delete %q and all its deployments? [y/N] ", slug)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			fmt.Println("aborted")
			return 1
		}
	}
	if err := client.DeleteApp(context.Background(), slug); err != nil {
		return printErr("Delete failed", err)
	}
	fmt.Printf("✓ Deleted %s\n", slug)
	return 0
}

// cmdDeployTarball extends cmdDeploy with `--tarball`, `--runtime`, `--handler`,
// `--dockerfile`. Image digest stays as the default input.
func cmdDeployTarball(args []string) int {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	image := fs.String("image", "", "digest-pinned image reference")
	tarball := fs.String("tarball", "", "path to source archive (tar.gz)")
	dockerfile := fs.Bool("dockerfile", false, "build with the supplied Dockerfile inside --tarball")
	runtime := fs.String("runtime", "", "function runtime (node22|python312)")
	handler := fs.String("handler", "", "function handler (e.g. handler.handler)")
	name := fs.String("name", "", "app name (default: current directory)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	slug := *name
	if slug == "" {
		slug = deriveName()
	}
	if *image == "" && *tarball == "" {
		fmt.Fprintln(os.Stderr, "✗ one of --image or --tarball is required.")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	if _, err := client.CreateApp(ctx, api.CreateAppRequest{Slug: slug}); err != nil {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Problem.Status != 409 {
			return printErr("Could not create app", err)
		}
	}

	if *tarball != "" {
		dep, err := client.DeployTarball(ctx, slug, *tarball, *runtime, *handler, *dockerfile)
		if err != nil {
			return printErr("Deploy failed", err)
		}
		fmt.Printf("✓ Queued build %s for %s\n", dep.ID, slug)
		return 0
	}
	dep, err := client.Deploy(ctx, slug, api.CreateDeploymentRequest{Image: *image})
	if err != nil {
		return printErr("Deploy failed", err)
	}
	fmt.Printf("✓ Deploying %s (%s)\n", slug, dep.Status)
	return 0
}

// cmdRollback, cmdPark, cmdWake implement their eponymous routes.
func cmdRollback(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: faas rollback <slug>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	dep, err := client.Rollback(context.Background(), args[0])
	if err != nil {
		return printErr("Rollback failed", err)
	}
	fmt.Printf("✓ Rolled back to %s (%s)\n", dep.ID, dep.Status)
	return 0
}

func cmdPark(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: faas park <slug>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.Park(context.Background(), args[0]); err != nil {
		return printErr("Park failed", err)
	}
	fmt.Println("✓ Parked (cold)")
	return 0
}

func cmdWake(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: faas wake <slug>")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.Wake(context.Background(), args[0]); err != nil {
		return printErr("Wake failed", err)
	}
	fmt.Println("✓ Waking…")
	return 0
}

// cmdDomains dispatches list/add/rm. Adding prints the TXT record the
// customer must publish for verification (spec §7).
func cmdDomains(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: faas domains <list|add|rm> [args]")
		return 1
	}
	switch args[0] {
	case subList:
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		out, err := client.ListDomains(context.Background())
		if err != nil {
			return printErr("Request failed", err)
		}
		for _, d := range out {
			verified := statusPending
			if d.Verified {
				verified = statusVerified
			}
			fmt.Printf("%-40s %-12s %s\n", d.Domain, verified, d.AppID)
		}
		return 0
	case subAdd:
		fs := flag.NewFlagSet("domains-add", flag.ContinueOnError)
		domain := fs.String("domain", "", "domain to attach (required)")
		slug := fs.String("app", "", "app slug to attach to (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *domain == "" || *slug == "" {
			fmt.Fprintln(os.Stderr, "usage: faas domains add --domain <d> --app <slug>")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		d, err := client.CreateDomain(context.Background(), api.CreateCustomDomainRequest{Domain: *domain, AppID: *slug})
		if err != nil {
			return printErr("Could not add domain", err)
		}
		fmt.Printf("Add this TXT record to your DNS:\n\n")
		fmt.Printf("  _faas-verify.%s  TXT  %s\n\n", d.Domain, d.ChallengeToken)
		fmt.Printf("Then run 'faas domains list' to see when verification completes.\n")
		return 0
	case subRm:
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: faas domains rm <domain>")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		if err := client.DeleteDomain(context.Background(), args[1]); err != nil {
			return printErr("Delete failed", err)
		}
		fmt.Println("✓ Removed")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown domains subcommand %q\n", args[0])
	return 1
}

// cmdCrons: list/add/rm.
func cmdCrons(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: faas crons <list|add|rm> [args]")
		return 1
	}
	switch args[0] {
	case subList:
		fs := flag.NewFlagSet("crons-list", flag.ContinueOnError)
		slug := fs.String("app", "", "app slug (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *slug == "" {
			fmt.Fprintln(os.Stderr, "usage: faas crons list --app <slug>")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		out, err := client.ListCrons(context.Background(), *slug)
		if err != nil {
			return printErr("Request failed", err)
		}
		for _, c := range out {
			state := "enabled"
			if !c.Enabled {
				state = "disabled"
			}
			fmt.Printf("%-30s %-15s %s\n", c.Schedule, state, c.Path)
		}
		return 0
	case subAdd:
		fs := flag.NewFlagSet("crons-add", flag.ContinueOnError)
		slug := fs.String("app", "", "app slug (required)")
		schedule := fs.String("schedule", "", "cron expression (required)")
		path := fs.String("path", "/", "request path")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *slug == "" || *schedule == "" {
			fmt.Fprintln(os.Stderr, "usage: faas crons add --app <slug> --schedule '*/5 * * * *' [--path /]")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		c, err := client.CreateCron(context.Background(), *slug, api.CreateCronRequest{
			AppID: *slug, Schedule: *schedule, Path: *path, Enabled: boolPtr(true),
		})
		if err != nil {
			return printErr("Create failed", err)
		}
		fmt.Printf("✓ Cron scheduled: %s %s\n", c.Schedule, c.Path)
		return 0
	case subRm:
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: faas crons rm <id>")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		if err := client.DeleteCron(context.Background(), args[1]); err != nil {
			return printErr("Delete failed", err)
		}
		fmt.Println("✓ Removed")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown crons subcommand %q\n", args[0])
	return 1
}

// cmdKeys: list/add/rm. Adding returns the plaintext token once (spec §2.2).
func cmdKeys(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: faas keys <list|add|rm> [args]")
		return 1
	}
	switch args[0] {
	case subList:
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		out, err := client.ListKeys(context.Background())
		if err != nil {
			return printErr("Request failed", err)
		}
		for _, k := range out {
			fmt.Printf("%-30s %s\n", k.Label, k.Prefix)
		}
		return 0
	case subAdd:
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: faas keys add <label>")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		k, err := client.CreateKey(context.Background(), args[1])
		if err != nil {
			return printErr("Create failed", err)
		}
		fmt.Printf("✓ New API key (shown ONCE):\n  %s\n", k.Plaintext)
		return 0
	case subRm:
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: faas keys rm <id>")
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		if err := client.DeleteKey(context.Background(), args[1]); err != nil {
			return printErr("Delete failed", err)
		}
		fmt.Println("✓ Removed")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown keys subcommand %q\n", args[0])
	return 1
}

// cmdUsage: GET /v1/usage?month=YYYY-MM. Defaults to the current month.
func cmdUsage(args []string) int {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	month := fs.String("month", "", "month (YYYY-MM); default: current month")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *month == "" {
		*month = time.Now().UTC().Format("2006-01")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	u, err := client.GetUsage(context.Background(), *month)
	if err != nil {
		return printErr("Request failed", err)
	}
	fmt.Printf("App %s — %d requests · %.3f GB-hours (included %d)\n", u.AppID, u.Requests, float64(u.MBSeconds)/3.6e6, u.IncludedGBHours)
	return 0
}

func boolPtr(b bool) *bool { return &b }

// cmdLogs: tail app or deployment logs via SSE. Minimal client-side parser;
// we just print lines with a timestamp prefix.
func cmdLogs(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow new lines")
	deployment := fs.String("deployment", "", "deployment id (default: latest)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: faas logs <slug> [--follow] [--deployment ID]")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	path := "/v1/apps/" + slug + "/logs?follow=" + boolStr(*follow)
	if *deployment != "" {
		path += "&deployment=" + *deployment
	}
	req, err := http.NewRequest("GET", client.baseURL+path, nil)
	if err != nil {
		return printErr("Bad request", err)
	}
	req.Header.Set("Authorization", "Bearer "+client.token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.http.Do(req)
	if err != nil {
		return printErr("Could not reach the API", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		var p api.Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			fmt.Fprintln(os.Stderr, (&APIError{Problem: p}).Error())
			return exitCodeForStatus(p.Status)
		}
		return printErr("Logs failed", fmt.Errorf("status %d", resp.StatusCode))
	}
	dec := newSSELineReader(resp.Body)
	for {
		line, err := dec.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			return printErr("Stream closed", err)
		}
		fmt.Println(line)
	}
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// sseLineReader peels "data: <line>\n\n" off a text/event-stream.
type sseLineReader struct {
	r   io.Reader
	buf []byte
}

func newSSELineReader(r io.Reader) *sseLineReader { return &sseLineReader{r: r} }

func (s *sseLineReader) Next() (string, error) {
	for {
		prefix, err := s.readUntil(":")
		if err != nil {
			return "", err
		}
		if prefix != "data" {
			continue
		}
		// consume " " after the colon, then read until \n
		_, _ = s.readByte() // ' '
		line, err := s.readUntil("\n")
		if err != nil {
			return "", err
		}
		// drain trailing blank line (\n\n)
		_, _ = s.readByte()
		return line, nil
	}
}

func (s *sseLineReader) readByte() (byte, error) {
	for len(s.buf) == 0 {
		if err := s.fill(); err != nil {
			return 0, err
		}
	}
	b := s.buf[0]
	s.buf = s.buf[1:]
	return b, nil
}

func (s *sseLineReader) readUntil(delim string) (string, error) {
	var b strings.Builder
	for {
		for _, d := range delim {
			_ = d
		}
		idx := strings.Index(string(s.buf), delim)
		if idx >= 0 {
			b.Write(s.buf[:idx])
			s.buf = s.buf[idx+len(delim):]
			return b.String(), nil
		}
		b.Write(s.buf)
		s.buf = nil
		if err := s.fill(); err != nil {
			if err == io.EOF && b.Len() > 0 {
				return b.String(), nil
			}
			return b.String(), err
		}
	}
}

func (s *sseLineReader) fill() error {
	tmp := make([]byte, 1024)
	n, err := s.r.Read(tmp)
	if n > 0 {
		s.buf = append(s.buf, tmp[:n]...)
	}
	if err == io.EOF && len(s.buf) > 0 {
		return nil
	}
	return err
}
