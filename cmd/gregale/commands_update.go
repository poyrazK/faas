package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/onebox-faas/faas/pkg/wire"
)

// `gregale update` replaces the running CLI with a newer release. It reads
// the same one-line cli-channel pointers as scripts/install.sh
// (ADR-20261025092617) through releases/download, which GitHub does not
// count against its anonymous API limit, and it verifies the archive against
// the release's CLI-SHA256SUMS before anything is written next to the
// binary. The new binary must run and report the expected version before it
// is renamed over the old one, so a failed update leaves the old CLI in place.

const (
	updateDefaultDownloadBase = "https://github.com/poyrazK/faas/releases/download"
	updateInstallerCommand    = "curl -fsSL https://get.gregale.dev | sh"
	// The release archive holds one ~50 MB binary; these bound a hostile or
	// broken server, not a real release.
	updateMaxArchiveBytes = 256 << 20
	updateMaxBinaryBytes  = 512 << 20
	updateMaxSumsBytes    = 1 << 20
	updateMaxPointerBytes = 256
	updateDownloadTimeout = 10 * time.Minute
	updateSmokeTimeout    = 30 * time.Second
)

// updateReleaseTag accepts the tags release.yml publishes: v<semver> with an
// optional prerelease, nothing that could escape a URL path segment.
var updateReleaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// Test seams.
var (
	updateExecutable = os.Executable
	updateGOOS       = runtime.GOOS
	updateGOARCH     = runtime.GOARCH
	updateHTTPClient = func() *http.Client { return &http.Client{Timeout: updateDownloadTimeout} }
)

type updateReport struct {
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Updated         bool   `json:"updated"`
	Channel         string `json:"channel"`
	Path            string `json:"path,omitempty"`
	Next            string `json:"next,omitempty"`
}

type cliUpdater struct {
	client       *http.Client
	downloadBase string
	channelBase  string
	goos, goarch string
}

func cmdUpdate(args []string) int {
	fs := newFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "report whether a newer release exists without installing it")
	version := fs.String("version", "", "install this exact release tag, including an older one")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	updater, err := newCLIUpdater()
	if err != nil {
		return printErr("Update unavailable", err)
	}
	current := updateCurrentVersion()
	report := updateReport{Current: wire.Version, Channel: "curl"}
	exe, exeErr := updateExecutablePath()
	if exeErr == nil {
		report.Path = exe
		if updateIsNPMInstall(exe) {
			report.Channel = "npm"
		}
	}
	if current == "" {
		report.Channel = "dev"
	}
	ctx := context.Background()

	var target string
	if *version != "" {
		target = updateNormalizeTag(*version)
		if !updateReleaseTag.MatchString(target) || !semver.IsValid(target) {
			return printErr("Invalid release tag", fmt.Errorf("--version %q is not a release tag such as v0.1.18", *version))
		}
	} else {
		target, err = updater.resolveLatest(ctx, current)
		if err != nil {
			return printErr("Could not find the newest release", err)
		}
	}
	report.Latest = target
	switch {
	case current == "":
		report.UpdateAvailable = true
	case *version != "":
		// An explicit tag may move backwards.
		report.UpdateAvailable = semver.Compare(target, current) != 0
	default:
		report.UpdateAvailable = semver.Compare(target, current) > 0
	}
	if !report.UpdateAvailable {
		message := fmt.Sprintf("gregale %s is the newest release.", wire.Version)
		if *version != "" {
			message = fmt.Sprintf("gregale %s is already installed.", wire.Version)
		}
		return updateFinish(report, message)
	}
	if *check {
		switch report.Channel {
		case "npm":
			report.Next = updateNPMCommand(target)
		case "dev":
			report.Next = "gregale update --version " + target
		default:
			report.Next = "gregale update"
		}
		return updateFinish(report, fmt.Sprintf("gregale %s is available (installed %s). Run: %s", target, wire.Version, report.Next))
	}
	switch {
	case report.Channel == "npm":
		report.Next = updateNPMCommand(target)
		return printErr("Installed with npm", fmt.Errorf("this gregale came from npm, which owns %s; update it with: %s", exe, report.Next))
	case current == "" && *version == "":
		return printErr("Development build", fmt.Errorf("this gregale is a development build (%s); pass --version <tag> to replace it with a release, or install one with: %s", wire.Version, updateInstallerCommand))
	case exeErr != nil:
		return printErr("Cannot locate this gregale", fmt.Errorf("%w; reinstall with: %s", exeErr, updateInstallerCommand))
	}
	if semver.Prerelease(target) != "" && (current == "" || semver.Prerelease(current) == "") {
		_, _ = fmt.Fprintf(osStderr, "warning: %s is a prerelease\n", target)
	}
	_, _ = fmt.Fprintf(osStderr, "Downloading gregale %s (%s/%s)…\n", target, updater.goos, updater.goarch)
	if err := updater.install(ctx, target, exe); err != nil {
		return printErr("Update failed", err)
	}
	report.Updated = true
	return updateFinish(report, fmt.Sprintf("Updated %s from %s to %s.", exe, wire.Version, target))
}

func updateFinish(report updateReport, message string) int {
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	_, _ = fmt.Fprintln(osStdout, message)
	return 0
}

func newCLIUpdater() (*cliUpdater, error) {
	switch updateGOOS + "/" + updateGOARCH {
	case "darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64":
	default:
		return nil, fmt.Errorf("gregale releases ship darwin and linux on amd64 and arm64, not %s/%s", updateGOOS, updateGOARCH)
	}
	download := strings.TrimRight(updateEnvOr("GREGALE_DOWNLOAD_BASE", updateDefaultDownloadBase), "/")
	channel := strings.TrimRight(updateEnvOr("GREGALE_CHANNEL_BASE", download+"/cli-channel"), "/")
	for _, base := range []string{download, channel} {
		if err := updateCheckBase(base); err != nil {
			return nil, err
		}
	}
	return &cliUpdater{client: updateHTTPClient(), downloadBase: download, channelBase: channel, goos: updateGOOS, goarch: updateGOARCH}, nil
}

func updateEnvOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// updateCheckBase refuses plain HTTP except on loopback: the checksum file
// comes from the same server as the archive, so the transport is what keeps
// a network attacker from swapping both.
func updateCheckBase(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("release download base %q is not a URL", base)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("release download base %q must use https", base)
}

// updateCurrentVersion is the running CLI's version as a comparable semver
// tag, or "" for a development build.
func updateCurrentVersion() string {
	v := updateNormalizeTag(wire.Version)
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

func updateNormalizeTag(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

func updateExecutablePath() (string, error) {
	exe, err := updateExecutable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

// updateIsNPMInstall reports whether exe is the binary of an
// @gregale/cli-<os>-<arch> npm package. Replacing it in place would leave
// npm's recorded version wrong, so npm installs are updated through npm.
func updateIsNPMInstall(exe string) bool {
	return strings.Contains(filepath.ToSlash(exe), "/node_modules/@gregale/cli-")
}

func updateNPMCommand(tag string) string {
	return "npm install -g gregale@" + strings.TrimPrefix(tag, "v")
}

func (u *cliUpdater) archiveName(tag string) string {
	return fmt.Sprintf("gregale_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), u.goos, u.goarch)
}

// resolveLatest picks the newest release to move to. Stable users follow
// CLI-STABLE, and CLI-LATEST only while no stable release lists their
// platform (the installer's order). Prerelease and development builds
// follow whichever pointer is newer.
func (u *cliUpdater) resolveLatest(ctx context.Context, current string) (string, error) {
	stable, stableErr := u.channelTag(ctx, "CLI-STABLE")
	followLatest := stableErr != nil || current == "" || semver.Prerelease(current) != ""
	if !followLatest {
		return stable, nil
	}
	latest, latestErr := u.channelTag(ctx, "CLI-LATEST")
	switch {
	case stableErr == nil && latestErr == nil:
		if semver.Compare(latest, stable) > 0 {
			return latest, nil
		}
		return stable, nil
	case stableErr == nil:
		return stable, nil
	case latestErr == nil:
		return latest, nil
	}
	return "", fmt.Errorf("no release channel names a release for %s/%s (%w); pass --version <tag>", u.goos, u.goarch, errors.Join(stableErr, latestErr))
}

// channelTag reads one cli-channel pointer and returns its tag when that
// release's CLI-SHA256SUMS lists this platform's archive.
func (u *cliUpdater) channelTag(ctx context.Context, pointer string) (string, error) {
	raw, err := u.get(ctx, u.channelBase+"/"+pointer, updateMaxPointerBytes)
	if err != nil {
		return "", fmt.Errorf("%s: %w", pointer, err)
	}
	tag := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	if !updateReleaseTag.MatchString(tag) || !semver.IsValid(tag) {
		return "", fmt.Errorf("%s does not name a release tag", pointer)
	}
	sums, err := u.get(ctx, u.downloadBase+"/"+tag+"/CLI-SHA256SUMS", updateMaxSumsBytes)
	if err != nil {
		return "", fmt.Errorf("%s names %s: %w", pointer, tag, err)
	}
	if _, ok := updateChecksumFor(sums, u.archiveName(tag)); !ok {
		return "", fmt.Errorf("%s names %s, which has no %s/%s archive", pointer, tag, u.goos, u.goarch)
	}
	return tag, nil
}

func (u *cliUpdater) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gregale/"+wire.Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GET %s: response larger than %d bytes", rawURL, limit)
	}
	return body, nil
}

// updateChecksumFor finds name's digest in a sha256sum listing.
func updateChecksumFor(sums []byte, name string) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		digest := strings.ToLower(fields[0])
		if decoded, err := hex.DecodeString(digest); err == nil && len(decoded) == sha256.Size {
			return digest, true
		}
	}
	return "", false
}

func (u *cliUpdater) install(ctx context.Context, tag, exe string) error {
	archive := u.archiveName(tag)
	sums, err := u.get(ctx, u.downloadBase+"/"+tag+"/CLI-SHA256SUMS", updateMaxSumsBytes)
	if err != nil {
		return fmt.Errorf("%s has no CLI-SHA256SUMS: %w", tag, err)
	}
	want, ok := updateChecksumFor(sums, archive)
	if !ok {
		return fmt.Errorf("CLI-SHA256SUMS for %s has no entry for %s", tag, archive)
	}
	body, err := u.get(ctx, u.downloadBase+"/"+tag+"/"+archive, updateMaxArchiveBytes)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s); nothing was installed", archive, want, got)
	}
	binary, err := updateExtractBinary(body)
	if err != nil {
		return fmt.Errorf("%s: %w", archive, err)
	}
	return updateReplaceExecutable(ctx, exe, binary, tag)
}

// updateExtractBinary returns the single "gregale" entry of a release archive.
func updateExtractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("archive has no gregale binary")
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(hdr.Name, "./") != "gregale" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size > updateMaxBinaryBytes {
			return nil, fmt.Errorf("gregale entry is %d bytes", hdr.Size)
		}
		return io.ReadAll(io.LimitReader(tr, updateMaxBinaryBytes))
	}
}

// updateReplaceExecutable writes binary beside exe, checks that it runs and
// reports tag, then renames it over exe. A running gregale keeps its open
// inode, as with the installer.
func updateReplaceExecutable(ctx context.Context, exe string, binary []byte, tag string) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".gregale-update-*")
	if err != nil {
		return updateWriteError(dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return updateWriteError(dir, err)
	}
	if err := tmp.Close(); err != nil {
		return updateWriteError(dir, err)
	}
	mode := fs.FileMode(0o755)
	if info, err := os.Stat(exe); err == nil {
		mode = info.Mode().Perm() | 0o111
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return updateWriteError(dir, err)
	}
	smokeCtx, cancel := context.WithTimeout(ctx, updateSmokeTimeout)
	defer cancel()
	out, err := exec.CommandContext(smokeCtx, tmpName, "version").Output() //nolint:gosec // G204: the binary was just verified against the release checksum.
	if err != nil {
		return fmt.Errorf("the downloaded %s did not run (%w); the installed gregale is unchanged", tag, err)
	}
	reported := strings.TrimSpace(string(out))
	if reported != "gregale "+tag && reported != "gregale "+strings.TrimPrefix(tag, "v") {
		return fmt.Errorf("the downloaded binary reports %q, not %s; the installed gregale is unchanged", reported, tag)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return updateWriteError(dir, err)
	}
	return nil
}

func updateWriteError(dir string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot write %s (%w); re-run with sudo, or install to a directory you own: %s -s -- --dir $HOME/.local/bin", dir, err, updateInstallerCommand)
	}
	return fmt.Errorf("cannot write %s: %w", dir, err)
}
