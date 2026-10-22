package main

// adr: 1190 — the wording of the emails an Auth app sends. Templates live in
// the app's own database and are edited through its /admin/v1/emails with an
// admin key; the Auth app validates and renders them.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const authEmailsUsage = "usage: gregale auth emails <list|show|set|reset|preview> NAME [TYPE] [--subject TEXT] [--body TEXT | --body-file FILE] [--send-to EMAIL] [--html FILE] [--admin-key-stdin]"

// authEmailTypes mirrors emailTypes in templates/auth/email.mjs
// (TestAuthEmailTypesContract).
var authEmailTypes = []string{"signup", "invite", "magiclink", "recovery", "email_change", "code", "security_notice", "organization_invitation", "signup_approved"}

type authEmailTemplate struct {
	Type         string   `json:"type"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	Custom       bool     `json:"custom"`
	Placeholders []string `json:"placeholders"`
	UpdatedAt    *string  `json:"updated_at"`
}

// authEmailFlags lists the flags each emails subcommand takes besides
// --admin-key-stdin.
var authEmailFlags = map[string][]string{"list": nil, "show": nil, "set": {"subject", "body", "body-file"}, "reset": nil, "preview": {"subject", "body", "body-file", "send-to", "html"}}

func cmdAuthEmails(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authEmailsUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth emails "+args[0], flag.ContinueOnError)
	subject := fs.String("subject", "", "the subject line; placeholders such as {{app}} are filled in")
	body := fs.String("body", "", "the body text; separate paragraphs with a blank line")
	bodyFile := fs.String("body-file", "", "read the body text from this file")
	sendTo := fs.String("send-to", "", "also send the preview to this address through your email provider")
	htmlFile := fs.String("html", "", "write the preview's HTML version to this file")
	keyStdin := fs.Bool("admin-key-stdin", false, "read the admin key from stdin (prompted on a terminal)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	allowed, known := authEmailFlags[args[0]]
	want := 2
	if args[0] == "list" {
		want = 1
	}
	if !known || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || (want == 2 && !slices.Contains(authEmailTypes, fs.Arg(1))) {
		PrintUsage(os.Stderr, authEmailsUsage, "auth")
		_, _ = fmt.Fprintf(osStderr, "Email types: %s\n", strings.Join(authEmailTypes, ", "))
		return 1
	}
	if err := authOnlyEmailFlags(fs, allowed); err != nil {
		return printErr("Invalid flags", err)
	}
	draft, err := authEmailDraft(fs, *subject, *body, *bodyFile, *keyStdin)
	if err != nil {
		return printErr("Invalid template", err)
	}
	if args[0] == "set" && len(draft) == 0 {
		return printErr("Invalid template", errors.New("set needs --subject, --body or --body-file"))
	}
	key, _, err := readRealtimeEndpointSecret("", *keyStdin, "admin-key", "Auth admin key: ", 128, true)
	if err != nil || !authAdminKeyFormat.MatchString(key) {
		return printErr("Invalid admin key", errors.New("expected a key from `gregale auth admin-key create`"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	app, err := client.GetApp(ctx, fs.Arg(0))
	cancel()
	if err != nil {
		return printErr("Could not find the Auth app", err)
	}
	origin, err := authExternalOrigin(app)
	if err != nil {
		return printErr("Could not resolve the Auth app", err)
	}
	return authEmailsRun(origin, key, args[0], fs.Arg(1), draft, *sendTo, *htmlFile)
}

func authOnlyEmailFlags(fs *flag.FlagSet, allowed []string) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		if err == nil && f.Name != "admin-key-stdin" && !slices.Contains(allowed, f.Name) {
			err = fmt.Errorf("%s does not take --%s", fs.Name(), f.Name)
		}
	})
	return err
}

// authEmailDraft collects --subject and the body; the Auth app checks the
// placeholders and limits again.
func authEmailDraft(fs *flag.FlagSet, subject, body, bodyFile string, keyStdin bool) (map[string]string, error) {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	draft := map[string]string{}
	if given["subject"] {
		if strings.TrimSpace(subject) == "" || len([]rune(subject)) > api.AuthEmailSubjectMaxChars || strings.ContainsAny(subject, "\r\n") {
			return nil, fmt.Errorf("--subject takes 1 to %d characters on one line", api.AuthEmailSubjectMaxChars)
		}
		draft["subject"] = subject
	}
	if given["body"] && given["body-file"] {
		return nil, errors.New("--body and --body-file are mutually exclusive")
	}
	if given["body-file"] {
		if bodyFile == "-" && keyStdin {
			return nil, errors.New("--admin-key-stdin already reads stdin; give the body as a file")
		}
		var raw []byte
		var err error
		if bodyFile == "-" {
			raw, err = readLimitedStdin(api.AuthEmailBodyMaxBytes + 2)
		} else {
			raw, err = os.ReadFile(bodyFile)
		}
		if err != nil {
			return nil, err
		}
		body = string(raw)
		given["body"] = true
	}
	if given["body"] {
		if text := strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")); text == "" || len(text) > api.AuthEmailBodyMaxBytes {
			return nil, fmt.Errorf("the body takes 1 to %d bytes", api.AuthEmailBodyMaxBytes)
		}
		draft["body"] = body
	}
	return draft, nil
}

func authEmailsRun(origin, key, command, kind string, draft map[string]string, sendTo, htmlFile string) int {
	path := "/admin/v1/emails"
	if kind != "" {
		path += "/" + kind
	}
	switch command {
	case "list":
		var out struct {
			Emails []authEmailTemplate `json:"emails"`
		}
		if err := authAdminCall(http.MethodGet, origin, key, path, nil, &out); err != nil {
			return printErr("Could not list email templates", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		for _, e := range out.Emails {
			_, _ = fmt.Fprintf(osStdout, "%-24s %-8s %s\n", e.Type, map[bool]string{true: "custom", false: "default"}[e.Custom], e.Subject)
		}
		return 0
	case "preview":
		request := map[string]any{}
		for name, value := range draft {
			request[name] = value
		}
		if sendTo != "" {
			request["send_to"] = sendTo
		}
		var out struct {
			Subject string `json:"subject"`
			Text    string `json:"text"`
			HTML    string `json:"html"`
			Sent    bool   `json:"sent"`
		}
		if err := authAdminCall(http.MethodPost, origin, key, path+"/preview", request, &out); err != nil {
			return printErr("Could not preview the email", err)
		}
		if htmlFile != "" {
			if err := os.WriteFile(htmlFile, []byte(out.HTML), 0o600); err != nil {
				return printErr("Could not write the HTML", err)
			}
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Subject: %s\n\n%s\n", out.Subject, out.Text)
		if out.Sent {
			PrintOK(osStdout, "Sent the preview to %s", sendTo)
		}
		return 0
	}
	method, verb := http.MethodGet, "read"
	var request any
	switch command {
	case "set":
		method, verb, request = http.MethodPatch, "save", draft
	case "reset":
		method, verb = http.MethodDelete, "reset"
	}
	var out authEmailTemplate
	if err := authAdminCall(method, origin, key, path, request, &out); err != nil {
		return printErr("Could not "+verb+" the email template", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	switch command {
	case "set":
		PrintOK(osStdout, "Saved the %s email; it is used from the next one sent", kind)
	case "reset":
		PrintOK(osStdout, "The %s email uses the default wording again", kind)
	}
	_, _ = fmt.Fprintf(osStdout, "Subject: %s\n\n%s\n\nPlaceholders: %s\n", out.Subject, out.Body, authEmailPlaceholders(out.Placeholders))
	return 0
}

func authEmailPlaceholders(names []string) string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = "{{" + name + "}}"
	}
	return strings.Join(out, " ")
}
