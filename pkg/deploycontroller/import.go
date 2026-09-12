package deploycontroller

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

const legacyMigrate = "migrate"

var legacyBinaries = []string{
	"apid", "schedd", "gatewayd", "gatewayd-internal", "gatewayd-public",
	"builderd", "imaged", "meterd", "githubd", "vmmd", "realtimed", "gregale", "hostage-gen",
	legacyMigrate, "deployctl",
}

func ImportLegacyBin(sourceDir, releasesRoot, releaseID, commitSHA string, now time.Time) (releasebundle.Manifest, error) {
	if sourceDir == "" || releasesRoot == "" || releaseID == "" || commitSHA == "" {
		return releasebundle.Manifest{}, fmt.Errorf("deploycontroller: incomplete legacy import arguments")
	}
	destination := filepath.Join(releasesRoot, releaseID)
	if _, err := os.Stat(destination); err == nil {
		return releasebundle.Manifest{}, fmt.Errorf("deploycontroller: legacy import destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return releasebundle.Manifest{}, fmt.Errorf("deploycontroller: inspect import destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(destination, "bin"), 0o755); err != nil {
		return releasebundle.Manifest{}, fmt.Errorf("deploycontroller: create import destination: %w", err)
	}
	for _, name := range legacyBinaries {
		source := filepath.Join(sourceDir, name)
		info, err := os.Stat(source)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return releasebundle.Manifest{}, fmt.Errorf("deploycontroller: inspect legacy binary %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return releasebundle.Manifest{}, fmt.Errorf("deploycontroller: legacy binary %s is not executable", name)
		}
		if err := copyExecutable(source, filepath.Join(destination, "bin", name), info.Mode().Perm()); err != nil {
			return releasebundle.Manifest{}, fmt.Errorf("deploycontroller: import %s: %w", name, err)
		}
	}
	manifest, err := releasebundle.Build(destination, releaseID, commitSHA, "linux/amd64", now)
	if err != nil {
		return releasebundle.Manifest{}, err
	}
	if err := releasebundle.Write(destination, manifest); err != nil {
		return releasebundle.Manifest{}, err
	}
	if err := releasebundle.Verify(destination, manifest); err != nil {
		return releasebundle.Manifest{}, err
	}
	return manifest, nil
}

func copyExecutable(source, destination string, mode fs.FileMode) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".legacy-import-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
}
