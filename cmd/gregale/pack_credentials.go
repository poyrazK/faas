package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/secretscan"
)

// Deploying from local files (no Git repository, no origin remote, or
// --worktree) packs every file except the fixed build-artifact excludes and
// .gregaleignore entries. A .env, an .npmrc token or a private key next to the
// source used to be uploaded with only a warning; since the owner decision of
// 2026-11-16 (prod findings 2026-11-15) deploy leaves credential-looking files
// out of the upload, as `gregale dev` does for .env, and names them on stderr
// and in the --json receipt. A negated .gregaleignore rule (for example
// `!.npmrc`) uploads one anyway.

// envTemplateFiles are committed examples without values; excluding them
// would be noise.
var envTemplateFiles = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true}

// credentialFileNames are exact lower-case basenames that conventionally
// hold credentials.
var credentialFileNames = map[string]bool{
	".npmrc": true, ".pypirc": true, ".netrc": true, "_netrc": true, ".git-credentials": true,
	".dockercfg": true, ".htpasswd": true, ".pgpass": true,
	".aws-credentials": true, "aws-credentials": true, "credentials.json": true,
	"service-account.json": true, "service_account.json": true, "serviceaccount.json": true,
	"serviceaccountkey.json": true, "gcp-key.json": true,
}

// credentialFileExts are extensions of key and certificate-bundle files.
var credentialFileExts = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true}

// sshKeyPrefixes start the default OpenSSH private-key file names.
var sshKeyPrefixes = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}

// credentialFileName reports whether relSlash names a file that commonly
// holds credentials. Public keys (*.pub) are not credentials.
func credentialFileName(relSlash string) bool {
	base := path.Base(relSlash)
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return !envTemplateFiles[base]
	}
	lower := strings.ToLower(base)
	if credentialFileNames[lower] || credentialFileExts[path.Ext(lower)] {
		return true
	}
	// Google service-account and OAuth client keys keep a project-derived
	// name (my-proj-firebase-adminsdk-x1y2z-0a1b2c.json, client_secret_<id>.json).
	if path.Ext(lower) == ".json" && (strings.Contains(lower, "service-account") || strings.Contains(lower, "service_account") ||
		strings.Contains(lower, "adminsdk") || strings.HasPrefix(lower, "client_secret")) {
		return true
	}
	if strings.HasSuffix(base, ".pub") {
		return false
	}
	for _, prefix := range sshKeyPrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	// ~/.aws/credentials and ~/.docker/config.json copied into a project.
	return relSlash == ".aws/credentials" || strings.HasSuffix(relSlash, "/.aws/credentials") ||
		relSlash == ".docker/config.json" || strings.HasSuffix(relSlash, "/.docker/config.json")
}

// localEnvFileName reports whether relSlash is a local .env file (.env or
// .env.*), not a committed template without values.
func localEnvFileName(relSlash string) bool {
	base := path.Base(relSlash)
	return (base == ".env" || strings.HasPrefix(base, ".env.")) && !envTemplateFiles[base]
}

// devLocalEnvExcludes lists, as absolute paths, the local .env files the
// packer would upload from srcDir. `gregale dev` keeps them out of the
// developer upload (owner decision 2026-11-15) unless .gregaleignore (or
// .dockerignore for a Dockerfile build) re-includes one with a negated rule
// such as `!.env`.
func devLocalEnvExcludes(srcDir string, dockerfile bool, extraExcludes ...string) []string {
	return localFileExcludes(srcDir, dockerfile, func(_, rel string) bool { return localEnvFileName(rel) }, extraExcludes...)
}

// deployCredentialExcludes lists, as absolute paths, the credential-looking
// files a local-files deploy would upload from srcDir, except those a negated
// ignore rule re-includes. A .pem without a private key (a CA bundle) is not
// a credential.
func deployCredentialExcludes(srcDir string, dockerfile bool, extraExcludes ...string) []string {
	return localFileExcludes(srcDir, dockerfile, func(abs, rel string) bool {
		if !credentialFileName(rel) {
			return false
		}
		return strings.ToLower(path.Ext(rel)) != ".pem" || fileHoldsPrivateKey(abs)
	}, extraExcludes...)
}

// fileHoldsPrivateKey reports whether the PEM file at abs contains a private
// key block. An unreadable file counts as one, so it stays local.
func fileHoldsPrivateKey(abs string) bool {
	f, err := openCustomerFile(abs)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	return err != nil || bytes.Contains(data, []byte("PRIVATE KEY"))
}

// localFileExcludes lists, sorted and absolute, the regular files the packer
// would upload from srcDir that match reports, skipping any re-included by a
// negated ignore rule. A walk error yields none, since packing reports it.
func localFileExcludes(srcDir string, dockerfile bool, match func(abs, rel string) bool, extraExcludes ...string) []string {
	if resolved, err := filepath.EvalSymlinks(srcDir); err == nil {
		srcDir = resolved
	}
	rules := loadPackRules(srcDir, dockerfile)
	var entries []packEntry
	if err := collectPackEntries(srcDir, srcDir, "", rules, packExtraExcludeSet(srcDir, extraExcludes), &entries, 0); err != nil {
		return nil
	}
	var excluded []string
	for _, entry := range entries {
		if entry.info.IsDir() || rules.selection(entry.rel, false).reason == "negated ignore rule" {
			continue // a directory, or explicitly re-included
		}
		abs := filepath.Join(srcDir, filepath.FromSlash(entry.rel))
		if match(abs, entry.rel) {
			excluded = append(excluded, abs)
		}
	}
	sort.Strings(excluded)
	return excluded
}

// maxCredentialWarningNames bounds the file list in one note.
const maxCredentialWarningNames = 10

// relNames joins files relative to srcDir, slash-separated, capped at
// maxCredentialWarningNames with an "and N more" tail.
func relNames(srcDir string, files []string) string {
	if resolved, err := filepath.EvalSymlinks(srcDir); err == nil {
		srcDir = resolved
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		if rel, err := filepath.Rel(srcDir, file); err == nil {
			names = append(names, filepath.ToSlash(rel))
		}
	}
	if len(names) > maxCredentialWarningNames {
		names = append(names[:maxCredentialWarningNames], fmt.Sprintf("and %d more", len(names)-maxCredentialWarningNames))
	}
	return strings.Join(names, ", ")
}

// devLocalEnvNote tells the developer which local env files stayed out of
// the upload and how to send their values instead.
func devLocalEnvNote(srcDir string, files []string) string {
	return fmt.Sprintf("not uploading local env files (%s); pass --env-file to sync their values as developer secrets, or add a negated rule such as !.env to .gregaleignore to upload one", relNames(srcDir, files))
}

// deployCredentialNote tells the customer which files stayed out of a
// local-files upload and how to set their values or upload one anyway, or
// returns "" when there are none.
func deployCredentialNote(srcDir string, files []string) string {
	if len(files) == 0 {
		return ""
	}
	return fmt.Sprintf("not uploading %s, which may hold credentials; set runtime values with `gregale env push`, or add a negated rule such as !%s to .gregaleignore to upload one", relNames(srcDir, files), filepath.Base(files[0]))
}

// excludeDeployCredentials returns the pack excludes for a local-files
// deploy of srcDir (extraExcludes plus its credential-looking files) and the
// note naming the files left out, which it prints and the caller adds to the
// --json receipt.
func excludeDeployCredentials(srcDir string, dockerfile bool, extraExcludes ...string) ([]string, string) {
	credentials := deployCredentialExcludes(srcDir, dockerfile, extraExcludes...)
	note := deployCredentialNote(srcDir, credentials)
	if note != "" {
		PrintWarn(osStderr, "%s", note)
	}
	return append(append([]string(nil), extraExcludes...), credentials...), note
}

// withoutExcludedFindings drops secret-scan findings in files the upload
// leaves out: a .env that stays local is no reason to warn about, or under
// --secret-scan=strict refuse, the upload.
func withoutExcludedFindings(srcDir string, excludes []string, findings []secretscan.Finding, err error) ([]secretscan.Finding, error) {
	if len(excludes) == 0 {
		return findings, err
	}
	excluded := packExtraExcludeSet(srcDir, excludes)
	keep := func(in []secretscan.Finding) []secretscan.Finding {
		var out []secretscan.Finding
		for _, f := range in {
			if !excluded[filepath.ToSlash(filepath.Clean(f.File))] {
				out = append(out, f)
			}
		}
		return out
	}
	findings = keep(findings)
	var strict *StrictSecretScanError
	if errors.As(err, &strict) {
		strict.Findings = keep(strict.Findings)
		if len(strict.Findings) == 0 && strict.Err == nil {
			err = nil
		}
	}
	return findings, err
}
