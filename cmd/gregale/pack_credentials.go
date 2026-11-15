package main

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Deploying from local files (no Git repository, no origin remote, or
// --worktree) packs every file except the fixed build-artifact excludes and
// .gregaleignore entries, so a .env, an .npmrc token or a private key next to
// the source is uploaded with it. The upload is unchanged by design (user
// decision, CLI audit 2026-10-31); deploy only names those files, on stderr
// and in the --json receipt, so the customer can exclude them.

// envTemplateFiles are committed examples without values; warning about them
// would be noise.
var envTemplateFiles = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true}

// credentialFileNames are exact basenames that conventionally hold
// credentials.
var credentialFileNames = map[string]bool{
	".npmrc": true, ".pypirc": true, ".netrc": true, "_netrc": true, ".git-credentials": true,
	".dockercfg": true, ".htpasswd": true, ".pgpass": true,
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
	if credentialFileNames[base] || credentialFileExts[strings.ToLower(path.Ext(base))] {
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

// packedCredentialFiles lists, sorted, the regular files packDirToTarGzWithRoot
// would pack from srcDir that look like credentials. It applies the same
// selection rules; a walk error yields no names, since packing reports it.
func packedCredentialFiles(srcDir string, dockerfile bool, extraExcludes ...string) []string {
	if resolved, err := filepath.EvalSymlinks(srcDir); err == nil {
		srcDir = resolved
	}
	var entries []packEntry
	if err := collectPackEntries(srcDir, srcDir, "", loadPackRules(srcDir, dockerfile), packExtraExcludeSet(srcDir, extraExcludes), &entries, 0); err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if !entry.info.IsDir() && credentialFileName(entry.rel) {
			names = append(names, entry.rel)
		}
	}
	sort.Strings(names)
	return names
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
// such as `!.env`. `gregale deploy` keeps uploading them and only warns.
func devLocalEnvExcludes(srcDir string, dockerfile bool, extraExcludes ...string) []string {
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
		if entry.info.IsDir() || !localEnvFileName(entry.rel) {
			continue
		}
		if rules.selection(entry.rel, false).reason == "negated ignore rule" {
			continue // explicitly re-included
		}
		excluded = append(excluded, filepath.Join(srcDir, filepath.FromSlash(entry.rel)))
	}
	sort.Strings(excluded)
	return excluded
}

// devLocalEnvNote tells the developer which local env files stayed out of
// the upload and how to send their values instead.
func devLocalEnvNote(srcDir string, files []string) string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		if rel, err := filepath.Rel(srcDir, file); err == nil {
			names = append(names, filepath.ToSlash(rel))
		}
	}
	if len(names) > maxCredentialWarningNames {
		names = append(names[:maxCredentialWarningNames], fmt.Sprintf("and %d more", len(files)-maxCredentialWarningNames))
	}
	return fmt.Sprintf("not uploading local env files (%s); pass --env-file to sync their values as developer secrets, or add a negated rule such as !.env to .gregaleignore to upload one", strings.Join(names, ", "))
}

// maxCredentialWarningNames bounds the file list in one warning line.
const maxCredentialWarningNames = 10

// localCredentialWarning is the warning for credential-looking files in a
// local-files upload, or "" when there are none.
func localCredentialWarning(names []string) string {
	if len(names) == 0 {
		return ""
	}
	listed := names
	more := ""
	if len(listed) > maxCredentialWarningNames {
		listed = listed[:maxCredentialWarningNames]
		more = fmt.Sprintf(" and %d more", len(names)-maxCredentialWarningNames)
	}
	return fmt.Sprintf("deploying local files uploads %s%s, which may hold credentials; add them to .gregaleignore to keep them out of the upload, and set runtime values with `gregale env push`", strings.Join(listed, ", "), more)
}

// warnLocalCredentialFiles prints the warning for a local-files upload of
// srcDir and returns it for the --json receipt.
func warnLocalCredentialFiles(srcDir string, dockerfile bool, extraExcludes ...string) string {
	warning := localCredentialWarning(packedCredentialFiles(srcDir, dockerfile, extraExcludes...))
	if warning != "" {
		PrintWarn(osStderr, "%s", warning)
	}
	return warning
}
