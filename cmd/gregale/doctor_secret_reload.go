package main

import (
	"bufio"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// secretReloadLabelRegex matches the Dockerfile LABEL that opts an image into
// in-place secret reload (docs/secrets.md).
var secretReloadLabelRegex = regexp.MustCompile(`(?i)^\s*LABEL\s.*\bcom\.gregale\.secret-reload-signal\s*=`)

// doctorCheckSecretReload warns when an app declares lifecycle.after_restore
// but its Dockerfile does not opt into secret reload. A snapshot captures the
// process environment from snapshot_prepare, which has no bindings or
// secrets; after a restore the environment cannot change, so the app must
// reread FAAS_SECRETS_SNAPSHOT_FILE, and only reload-enabled images get it
// refreshed. Without the label, rotations and restores quietly fall back to
// cold boots (ChronoLake report). Present only when the app declares the hook
// and builds from a local Dockerfile; source builds have no label to read.
func doctorCheckSecretReload(path string) (doctorCheck, bool) {
	manifest, present, err := gregalemanifest.Load(path)
	if err != nil || !present || manifest == nil || manifest.Lifecycle == nil || manifest.Lifecycle.AfterRestore == nil {
		return doctorCheck{}, false
	}
	f, err := openCustomerFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		return doctorCheck{}, false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	line := 0
	firstFrom := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if secretReloadLabelRegex.MatchString(text) {
			return doctorCheck{Name: "secret-reload", Status: "ok"}, true
		}
		if firstFrom == 0 && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(text)), "FROM ") {
			firstFrom = line
		}
	}
	source := "Dockerfile"
	if firstFrom > 0 {
		source += ":" + strconv.Itoa(firstFrom)
	}
	return doctorCheck{
		Name:    "secret-reload",
		Status:  "warn",
		Code:    "after_restore_without_secret_reload",
		Hint:    "gregale.yaml declares lifecycle.after_restore, but the Dockerfile does not set the com.gregale.secret-reload-signal label.",
		Why:     "A snapshot keeps the environment it was captured with, which has no bindings or secrets. Only reload-enabled images get FAAS_SECRETS_SNAPSHOT_FILE refreshed after a restore or rotation; otherwise those wakes fall back to a slower cold boot.",
		Fix:     `Add LABEL com.gregale.secret-reload-signal="SIGHUP", reread FAAS_SECRETS_SNAPSHOT_FILE in your after_restore handler and on the signal, and keep the handler fast (no blocking database calls). See docs/secrets.md.`,
		Sources: []string{source},
	}, true
}
