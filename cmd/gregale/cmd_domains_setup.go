package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const domainsSetupUsage = "usage: gregale domains setup <domain> --app SLUG [--environment SLUG] [--timeout 10m] [--poll-interval 10s]"

func cmdDomainsSetup(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, domainsSetupUsage, "domains")
		return 0
	}
	fs := newFlagSet("domains setup", flag.ContinueOnError)
	slug := fs.String("app", "", "app to bind the domain to")
	environment := fs.String("environment", "", "project environment to route to")
	timeout := fs.Duration("timeout", 10*time.Minute, "verification wait deadline")
	interval := fs.Duration("poll-interval", 10*time.Second, "verification check interval (5s..1m)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !validCLISlug(*slug) || *environment != "" && !api.ValidProjectEnvironmentSlug(*environment) {
		PrintUsage(osStderr, domainsSetupUsage, "domains")
		return 1
	}
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(fs.Arg(0))), ".")
	if !validSetupDomain(domain) {
		return printErr("Invalid domain", errors.New("use a DNS hostname such as api.example.com, without a scheme, port, or path"))
	}
	if *timeout <= 0 || *timeout > time.Hour || *interval < 5*time.Second || *interval > time.Minute {
		return printErr("Invalid wait flags", errors.New("--timeout must be positive and at most 1h; --poll-interval must be 5s..1m"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("domains setup requires terminal input and output; use domains add, verify, and set-default for scripts or JSON"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	app, err := client.GetApp(requestCtx, *slug)
	if err != nil {
		cancel()
		return printErr("Could not read app", err)
	}
	domains, err := client.ListDomains(requestCtx)
	if err != nil {
		cancel()
		return printErr("Could not read domain bindings", err)
	}
	var binding api.CustomDomainResponse
	found := false
	for _, existing := range domains {
		if existing.Domain == domain {
			binding, found = existing, true
			break
		}
	}
	if found && (binding.AppID != app.ID || binding.Environment != *environment) {
		cancel()
		return printErr("Domain already bound elsewhere", errors.New("the existing app or environment does not match; inspect domains show before changing the binding"))
	}
	if !found {
		binding, err = client.CreateDomain(requestCtx, api.CreateCustomDomainRequest{Domain: domain, AppID: *slug, Environment: *environment})
	}
	cancel()
	if err != nil {
		return printErr("Could not bind domain", err)
	}
	if binding.Domain != domain || binding.AppID != app.ID || binding.Environment != *environment {
		return printErr("Invalid domain binding", errors.New("the response does not match the requested domain, app, and environment"))
	}
	_, _ = fmt.Fprintf(osStdout, "Domain: %s; app: %s\nAdd these records at your DNS provider:\n", domain, *slug)
	printDomainDNSRecords(osStdout, binding)
	_, _ = fmt.Fprintln(osStdout, "Keep the records in place for certificate issuance and renewal.")
	command := "gregale"
	if profile := currentProfile(); profile != "default" {
		command += " --profile '" + strings.ReplaceAll(profile, "'", "'\"'\"'") + "'"
	}
	resume := fmt.Sprintf("%s domains setup %s --app %s", command, domain, *slug)
	if *environment != "" {
		resume += " --environment " + *environment
	}
	resume += fmt.Sprintf(" --timeout %s --poll-interval %s", *timeout, *interval)
	_, _ = fmt.Fprintln(osStdout, "Continue later: "+resume)
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	check, err := prompt.confirm(ctx, "Ready to check DNS and wait for the certificate?")
	if err != nil {
		return startInputExit(err)
	}
	if !check {
		return 0
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, *timeout)
	defer waitCancel()
	lastState := ""
	for {
		binding, err = client.VerifyDomain(waitCtx, domain)
		if err != nil {
			_, _ = fmt.Fprintln(osStdout, "Continue checking: "+resume)
			if ctx.Err() != nil {
				return 130
			}
			return printErr("Domain verification did not finish", err)
		}
		if binding.Domain != domain || binding.AppID != app.ID || binding.Environment != *environment {
			return printErr("Domain binding changed", errors.New("the domain now points to a different app or environment; inspect domains show"))
		}
		if domainSetupReady(binding) {
			break
		}
		certStatus := binding.CertStatus
		if certStatus == "" {
			certStatus = "pending"
		}
		state := fmt.Sprintf("ownership verified=%t; certificate=%s; %s", binding.Verified, certStatus, binding.CertLastError)
		if state != lastState {
			PrintProgress(osStdout, "%s", state)
			if report, doctorErr := client.DomainDoctor(waitCtx, domain); doctorErr == nil && report.Domain == domain && report.AppID == app.ID {
				printDoctorReport(osStdout, report)
			}
			lastState = state
		}
		timer := time.NewTimer(*interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			_, _ = fmt.Fprintln(osStdout, "Verification is still pending. Continue checking: "+resume)
			if ctx.Err() != nil {
				return 130
			}
			return 1
		case <-timer.C:
		}
	}
	PrintOK(osStdout, "%s is verified and its TLS certificate is ready.", domain)
	if *environment != "" {
		PrintProgress(osStdout, "Domain setup is complete for environment %s.", *environment)
		return 0
	}
	if binding.Default {
		PrintProgress(osStdout, "This is already the app's default domain.")
		return 0
	}
	makeDefault, err := prompt.confirm(ctx, "Make "+domain+" the default domain for "+*slug+"?")
	if err != nil {
		return startInputExit(err)
	}
	if makeDefault {
		defaultCtx, defaultCancel := context.WithTimeout(ctx, 30*time.Second)
		defer defaultCancel()
		updated, err := client.SetDefaultDomain(defaultCtx, domain)
		if err != nil {
			return printErr("Domain verified, but default change failed", err)
		}
		if updated.AppID != app.ID || updated.Domain != domain || updated.Environment != *environment || !updated.Default {
			return printErr("Invalid default-domain response", errors.New("the response does not confirm the expected binding as default"))
		}
		PrintOK(osStdout, "Default domain: %s", domain)
	}
	return 0
}

func domainSetupReady(d api.CustomDomainResponse) bool {
	if !d.Verified {
		return false
	}
	if d.CertStatus != "" && d.CertStatus != "issued" && d.CertStatus != "renewing" {
		return false
	}
	// Verification of ownership alone does not establish HTTPS readiness.
	expires := d.CertNotAfter
	if expires == "" {
		expires = d.CertExpiresAt
	}
	end, err := time.Parse(time.RFC3339, expires)
	return err == nil && end.After(time.Now())
}

func validSetupDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") {
		return false
	}
	labelPattern := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || !labelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
