// commands_trusted_publishers.go — operator-side CLI for the per-app
// cosign trusted-publisher list (issue #472 / ADR-054). Mirrors the
// `gregale sign-keys` shape (commands_sign_keys.go) but lives on the
// CUSTOMER side: every leaf here calls authedClient() and hits the
// apid admin endpoints (PUT/DELETE/GET /v1/apps/{slug}/trusted_signers).
//
// The namespace `gregale trusted-publishers` deliberately does NOT
// collide with `gregale sign-keys` (the operator's BUILD-side signing
// key manager, commands_sign_keys.go). Two distinct surfaces:
//
//	sign-keys         → /etc/faas/secrets/{sign.key, sign-pub.pem}
//	                    operator-only, no API calls
//	trusted-publishers → /v1/apps/{slug}/trusted_signers/{name}
//	                    admin API key required, hits apid
//
// Subcommand surface (mirrors cmdKeys + cmdCrons shapes):
//
//	add <slug> <name> <pub.pem>   PUT   (admin)
//	remove <slug> <name>          DELETE (admin)
//	list <slug>                   GET    (admin)
//
// Future surface (out of scope for this PR; file as follow-ups):
//   - `rotate <slug> <name>` — explicit rotation log entry
//   - `--format=json`        — machine-readable list output
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const dispatchTrustedPublishers = "trusted-publishers"

const (
	subTrustedAdd    = "add"
	subTrustedRemove = "remove"
	subTrustedList   = "list"
)

// cmdTrustedPublishers is the parent dispatcher. With zero args it
// prints usage; with add/remove/list it fans to the matching helper.
// Unknown subcommands return 1 with a usage hint — same contract as
// cmdSignKeys / cmdKeys.
func cmdTrustedPublishers(args []string) int {
	parent, _ := lookupCliCommand("trusted-publishers")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale trusted-publishers <add|remove|list> [flags]", "trusted-publishers")
		return 1
	}
	switch args[0] {
	case subTrustedAdd:
		return cmdTrustedPublishersAdd(args[1:])
	case subTrustedRemove:
		return cmdTrustedPublishersRemove(args[1:])
	case subTrustedList:
		return cmdTrustedPublishersList(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale trusted-publishers: unknown subcommand %q (known: add, remove, list)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// cmdTrustedPublishersAdd uploads a cosign public key PEM to the
// per-app trusted-publisher list. The PEM is decoded to raw DER
// (the wire form apid expects — base64 of the SPKI blob, NOT a
// PEM-armoured block) and posted to PUT
// /v1/apps/{slug}/trusted_signers/{name}. Admin API key required;
// the SDK's authedClient() helper surfaces the 401/403 shape for
// missing or wrong-scope keys.
func cmdTrustedPublishersAdd(args []string) int {
	fs := flag.NewFlagSet("trusted-publishers add", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 3 {
		PrintUsage(os.Stderr, "usage: gregale trusted-publishers add <slug> <name> <pub.pem>", "trusted-publishers")
		return 1
	}
	slug, name, pemPath := rest[0], rest[1], rest[2]

	der, err := readCosignPublicKeyDER(pemPath)
	if err != nil {
		return printErr("read public key", err)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if err := client.PutAppTrustedSigner(ctx, slug, name, api.AddTrustedSignerRequest{
		PublicKeyPEM: base64.StdEncoding.EncodeToString(der),
	}); err != nil {
		return printErr("PUT trusted_signer", err)
	}
	PrintOK(osStdout, "trusted signer %q added to app %q (%d bytes)\n", name, slug, len(der))
	return 0
}

// cmdTrustedPublishersRemove deletes a (slug, name) row from the
// per-app trusted-publisher list. 404 is rendered as a non-fatal
// warning — removing a row that was already removed (a concurrent
// operator action) is idempotent at the wire surface.
func cmdTrustedPublishersRemove(args []string) int {
	fs := flag.NewFlagSet("trusted-publishers remove", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 2 {
		PrintUsage(os.Stderr, "usage: gregale trusted-publishers remove <slug> <name>", "trusted-publishers")
		return 1
	}
	slug, name := rest[0], rest[1]

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if err := client.DeleteAppTrustedSigner(ctx, slug, name); err != nil {
		// 404 = "already absent" — idempotent at the wire
		// surface. The SDK surfaces this as an *api.Problem with
		// Code == CodeTrustedSignerNotFound; we match on the code
		// string so the check survives error-message drift.
		if isTrustedSignerNotFound(err) {
			PrintOK(os.Stdout, "trusted signer %q already absent from app %q\n", name, slug)
			return 0
		}
		return printErr("DELETE trusted_signer", err)
	}
	PrintOK(os.Stdout, "trusted signer %q removed from app %q\n", name, slug)
	return 0
}

// isTrustedSignerNotFound reports whether the SDK's error carries
// the trusted_signer_not_found code. Uses errors.As to walk the
// chain — the SDK's *Problem wraps the underlying transport error.
func isTrustedSignerNotFound(err error) bool {
	var p *api.Problem
	if !errors.As(err, &p) {
		return false
	}
	return p.Code == api.CodeTrustedSignerNotFound
}

// cmdTrustedPublishersList prints every trusted-publisher on the
// app. Output is line-oriented (one row per signer). --json (future
// patch; out of scope here) would emit the raw
// AppTrustedSignerListResponse JSON for scripting.
func cmdTrustedPublishersList(args []string) int {
	fs := flag.NewFlagSet("trusted-publishers list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 1 {
		PrintUsage(os.Stderr, "usage: gregale trusted-publishers list <slug>", "trusted-publishers")
		return 1
	}
	slug := rest[0]

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	out, err := client.ListAppTrustedSigners(ctx, slug)
	if err != nil {
		return printErr("GET trusted_signers", err)
	}
	if len(out.Signers) == 0 {
		PrintOK(os.Stdout, "no trusted signers configured for app %q\n", slug)
		return 0
	}
	for _, s := range out.Signers {
		// Print first 12 chars of the base64 — operators can match
		// this against the key fingerprint `gregale sign-keys
		// status` reports for the host-side build key. The full
		// public key is recoverable via GET (the response carries
		// it).
		short := s.PublicKeyPEM
		if len(short) > 12 {
			short = short[:12] + "…"
		}
		PrintOK(os.Stdout, "  %s  %s  added_at=%s\n", s.Name, short, s.AddedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	return 0
}

// readCosignPublicKeyDER reads a PEM file and returns the raw DER
// SubjectPublicKeyInfo bytes. The handler expects the wire form to
// be base64(DER SPKI), so the PEM-armoured wrapper is stripped here
// before the encode. The DER byte length must land in [64, 1024] to
// satisfy the DB CHECK — apid enforces the same range on the server
// side, so a too-short / too-long file is rejected with
// trusted_signer_invalid.
//
// We intentionally do NOT use pkg/cosign.loadPublicKey here — that
// helper also enforces mode + curve (P-256 only). The CLI is the
// operator-side interface to the upload surface; apid's verification
// is the canonical mode/curve gate. If a future operator ships an
// RSA-2048 pub key, the upload should fail with a clear "P-256 only"
// message from apid, not a CLI-side silent strip.
func readCosignPublicKeyDER(path string) ([]byte, error) {
	cleaned := filepath.Clean(path)
	data, err := os.ReadFile(cleaned)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", cleaned, err)
	}
	// Find the first PEM block with Type "PUBLIC KEY". A future
	// enhancement could handle a multi-block file (some cosign
	// exports bundle a chain); today the single-block case is
	// the entire supported surface.
	const pemStart = "-----BEGIN PUBLIC KEY-----"
	const pemEnd = "-----END PUBLIC KEY-----"
	startIdx := strings.Index(string(data), pemStart)
	if startIdx < 0 {
		return nil, fmt.Errorf("file does not contain a BEGIN PUBLIC KEY block (path=%q)", cleaned)
	}
	endIdx := strings.Index(string(data), pemEnd)
	if endIdx < 0 {
		return nil, fmt.Errorf("file has BEGIN without END PUBLIC KEY (path=%q)", cleaned)
	}
	armored := data[startIdx : endIdx+len(pemEnd)]
	der := make([]byte, base64.StdEncoding.DecodedLen(len(armored)))
	n, err := base64.StdEncoding.Decode(der, extractPEMBody(armored))
	if err != nil {
		return nil, fmt.Errorf("decode PEM body (path=%q): %w", cleaned, err)
	}
	return der[:n], nil
}

// extractPEMBody strips the BEGIN/END armour lines and returns the
// base64 body lines joined with newlines. The cosign convention
// emits 64 chars per line; we don't reformat, we just join.
func extractPEMBody(armored []byte) []byte {
	var out []byte
	for _, line := range bytesSplit(armored, '\n') {
		trimmed := bytesTrim(line)
		if bytesHasPrefix(trimmed, []byte("-----")) {
			continue
		}
		out = append(out, trimmed...)
	}
	return out
}

// bytesSplit / bytesTrim / bytesHasPrefix are tiny inline helpers
// kept local so the file compiles without importing bytes. They
// exist because the cmd/gregale package's import set already pulls
// in stdlib heavily and the PEM strip is the only bytes.* call site
// in this file.
func bytesSplit(b []byte, sep byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == sep {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	out = append(out, b[start:])
	return out
}

func bytesTrim(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}

func bytesHasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}
