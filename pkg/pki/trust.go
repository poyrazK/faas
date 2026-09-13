package pki

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ValidateTrustBundle verifies the material that may be copied to a host
// which does not own the fleet CA. The bundle contains the public CA
// certificate and the leaves needed by the selected box role, but never the
// CA private key. It is deliberately read-only: a missing or stale leaf is a
// deployment error rather than an invitation for a remote host to mint one.
func ValidateTrustBundle(rootDir, hostRole string, extraSANs AltNames) error {
	return validateTrustBundle(rootDir, hostRole, extraSANs, "")
}

// ValidateTrustBundleForNode is the topology-aware trust-bundle validator.
// In addition to the canonical daemon CNs, it requires compute-only leaves
// that connect to control-plane node verifiers to use nodeCN. That CN is the
// compute_nodes identity used by the handshake verifier; accepting a generic
// daemon CN here would let a bundle pass staging and fail in production.
func ValidateTrustBundleForNode(rootDir, hostRole string, extraSANs AltNames, nodeCN string) error {
	return validateTrustBundle(rootDir, hostRole, extraSANs, nodeCN)
}

// IssueTrustBundle creates a host-scoped, trust-only bundle from operator
// issuance material. The CA private key is read only from issuerRoot and is
// never written below outputRoot. Each invocation reissues the selected leaf
// set so node identity and transport SANs cannot leak between fleet members.
func IssueTrustBundle(issuerRoot, outputRoot, hostRole, nodeCN string, extraSANs AltNames) error {
	if hostRole == "compute-only" && nodeCN == "" {
		return errors.New("pki: compute-only trust bundle requires a node identity")
	}
	if err := ValidateIssuanceMaterial(issuerRoot, hostRole); err != nil {
		return err
	}
	issuerCertPath, issuerKeyPath := CARoot(issuerRoot)
	caCert, caKey, err := loadExistingCA(issuerCertPath, issuerKeyPath)
	if err != nil {
		return err
	}
	if err := createSafeStagingRoot(outputRoot, issuerRoot); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(outputRoot, "ca"), 0o755); err != nil {
		return fmt.Errorf("pki: create trust bundle CA directory: %w", err)
	}
	outputCertPath, outputKeyPath := CARoot(outputRoot)
	caPEM, err := os.ReadFile(issuerCertPath)
	if err != nil {
		return fmt.Errorf("pki: read issuer CA certificate: %w", err)
	}
	if err := writeFileAtomic(outputCertPath, caPEM, 0o444, -1, -1); err != nil {
		return fmt.Errorf("pki: write trust bundle CA certificate: %w", err)
	}
	// A stale output directory must never turn a trust-only bundle into an
	// issuer. Removal is safe even when the path does not exist.
	if err := os.Remove(outputKeyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("pki: remove CA private key from trust bundle: %w", err)
	}
	for _, role := range RolesForBox(hostRole) {
		commonName := role.CommonName
		if hostRole == "compute-only" && RoleUsesNodeIdentity(role) {
			commonName = nodeCN
		}
		if err := ensureLeafWithIdentity(outputRoot, role, commonName, caCert, caKey, true, extraSANs); err != nil {
			return fmt.Errorf("pki: issue trust bundle leaf %s/%s: %w", role.Directory, role.Filename, err)
		}
	}
	if err := ValidateTrustBundleForNode(outputRoot, hostRole, extraSANs, nodeCN); err != nil {
		return fmt.Errorf("pki: validate issued trust bundle: %w", err)
	}
	return nil
}

// RenewTrustBundle copies a host's complete active trust set into a fresh
// staging root and reissues only leaves inside ReissueThreshold (or whose
// identity/SAN contract drifted). Safe leaves retain their serial and key.
// The active root never needs CA issuance material and the returned roles are
// the exact consumer set the orchestrator must reload.
func RenewTrustBundle(issuerRoot, activeRoot, outputRoot, hostRole, nodeCN string, extraSANs AltNames) ([]Role, error) {
	if hostRole == "compute-only" && nodeCN == "" {
		return nil, errors.New("pki: compute-only trust bundle requires a node identity")
	}
	if err := ValidateIssuanceMaterial(issuerRoot, hostRole); err != nil {
		return nil, err
	}
	if err := ValidateTrustBundleForNode(activeRoot, hostRole, extraSANs, nodeCN); err != nil {
		return nil, fmt.Errorf("pki: validate active trust bundle: %w", err)
	}
	if err := createSafeStagingRoot(outputRoot, issuerRoot, activeRoot); err != nil {
		return nil, err
	}
	if err := copyTrustBundle(activeRoot, outputRoot, hostRole); err != nil {
		return nil, err
	}
	issuerCertPath, issuerKeyPath := CARoot(issuerRoot)
	activeCertPath, _ := CARoot(activeRoot)
	issuerCA, err := os.ReadFile(issuerCertPath)
	if err != nil {
		return nil, fmt.Errorf("pki: read issuer CA certificate: %w", err)
	}
	activeCA, err := os.ReadFile(activeCertPath)
	if err != nil {
		return nil, fmt.Errorf("pki: read active CA certificate: %w", err)
	}
	if !bytes.Equal(issuerCA, activeCA) {
		return nil, errors.New("pki: active CA differs from issuer CA")
	}
	caCert, caKey, err := loadExistingCA(issuerCertPath, issuerKeyPath)
	if err != nil {
		return nil, err
	}
	var changed []Role
	for _, role := range RolesForBox(hostRole) {
		commonName := role.CommonName
		if hostRole == "compute-only" && RoleUsesNodeIdentity(role) {
			commonName = nodeCN
		}
		err := ensureLeafWithIdentity(outputRoot, role, commonName, caCert, caKey, false, extraSANs)
		switch {
		case err == nil:
			changed = append(changed, role)
		case errors.Is(err, ErrLeafNotExpiringSoon):
		default:
			return nil, fmt.Errorf("pki: renew trust bundle leaf %s/%s: %w", role.Directory, role.Filename, err)
		}
	}
	if len(changed) == 0 {
		return nil, errors.New("pki: renewal requested but no leaf is inside the renewal threshold")
	}
	if err := ValidateTrustBundleForNode(outputRoot, hostRole, extraSANs, nodeCN); err != nil {
		return nil, fmt.Errorf("pki: validate renewed trust bundle: %w", err)
	}
	return changed, nil
}

// ExportTrustBundle copies only the public CA and role-specific leaf pairs to
// a fresh root. It is the safe source for remote-to-issuer renewal transfer;
// ca.key is neither read nor copied.
func ExportTrustBundle(sourceRoot, outputRoot, hostRole, nodeCN string, extraSANs AltNames) error {
	if err := ValidateTrustBundleForNode(sourceRoot, hostRole, extraSANs, nodeCN); err != nil {
		return err
	}
	if err := createSafeStagingRoot(outputRoot, sourceRoot); err != nil {
		return err
	}
	return copyTrustBundle(sourceRoot, outputRoot, hostRole)
}

func copyTrustBundle(sourceRoot, outputRoot, hostRole string) error {
	if err := os.Mkdir(filepath.Join(outputRoot, "ca"), 0o755); err != nil {
		return fmt.Errorf("pki: create exported CA directory: %w", err)
	}
	sourceCA, _ := CARoot(sourceRoot)
	targetCA, targetCAKey := CARoot(outputRoot)
	caPEM, err := os.ReadFile(sourceCA)
	if err != nil {
		return fmt.Errorf("pki: read source CA: %w", err)
	}
	if err := writeFileAtomic(targetCA, caPEM, 0o444, -1, -1); err != nil {
		return fmt.Errorf("pki: copy source CA: %w", err)
	}
	_ = os.Remove(targetCAKey)
	for _, role := range RolesForBox(hostRole) {
		sourceCert, sourceKey := LeafPaths(sourceRoot, role)
		targetCert, targetKey := LeafPaths(outputRoot, role)
		for _, file := range []struct {
			source string
			target string
			mode   os.FileMode
		}{{sourceCert, targetCert, 0o444}, {sourceKey, targetKey, 0o400}} {
			body, err := os.ReadFile(file.source)
			if err != nil {
				return fmt.Errorf("pki: read trust file %q: %w", file.source, err)
			}
			if err := writeFileAtomic(file.target, body, file.mode, -1, -1); err != nil {
				return fmt.Errorf("pki: copy trust file %q: %w", file.target, err)
			}
		}
	}
	return nil
}

func createSafeStagingRoot(outputRoot string, protectedRoots ...string) error {
	if strings.TrimSpace(outputRoot) == "" {
		return errors.New("pki: staging root is empty")
	}
	if _, err := os.Lstat(outputRoot); err == nil {
		return fmt.Errorf("pki: staging root %q already exists", outputRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("pki: inspect staging root %q: %w", outputRoot, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(outputRoot))
	if err != nil {
		return fmt.Errorf("pki: resolve staging parent: %w", err)
	}
	for _, root := range protectedRoots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return fmt.Errorf("pki: resolve protected root %q: %w", root, err)
		}
		if samePathOrDescendant(parent, resolved) {
			return fmt.Errorf("pki: staging root must not be inside protected root %q", root)
		}
	}
	if err := os.Mkdir(outputRoot, 0o700); err != nil {
		return fmt.Errorf("pki: create staging root: %w", err)
	}
	info, err := os.Lstat(outputRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pki: staging root %q is not a new directory", outputRoot)
	}
	return nil
}

func samePathOrDescendant(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}

const installJournalFilename = ".pki-install-journal.json"

type installFile struct {
	Destination string `json:"destination"`
	Staged      string `json:"staged"`
	Backup      string `json:"backup"`
}

type installJournal struct {
	Phase string        `json:"phase"`
	Files []installFile `json:"files"`
}

// HasPendingInstall reports whether a previous install still needs recovery
// or committed-artifact cleanup. Status callers use it to avoid declaring a
// host healthy while an interrupted on-disk certificate transaction exists.
func HasPendingInstall(targetRoot string) bool {
	_, err := os.Lstat(filepath.Join(targetRoot, installJournalFilename))
	return err == nil
}

// RecoverPendingInstall restores an interrupted applying transaction or
// removes leftovers from a transaction already marked committed.
func RecoverPendingInstall(targetRoot string) error {
	return recoverInstallJournal(targetRoot)
}

// InstallTrustBundle replaces every selected leaf as one validated batch. It
// rejects a CA change, stages all bytes before touching live paths, preserves
// the live file mode and ownership, and rolls the full batch back if any
// rename or final validation fails. Daemons continue using their in-memory
// certificates until the orchestrator reloads them after this function
// returns, so they never observe the short on-disk pair transition.
func InstallTrustBundle(bundleRoot, targetRoot, hostRole, nodeCN string, extraSANs AltNames) error {
	if hostRole == "compute-only" && nodeCN == "" {
		return errors.New("pki: compute-only trust bundle install requires a node identity")
	}
	if err := recoverInstallJournal(targetRoot); err != nil {
		return fmt.Errorf("pki: recover interrupted trust bundle install: %w", err)
	}
	if err := ValidateTrustBundleForNode(bundleRoot, hostRole, extraSANs, nodeCN); err != nil {
		return fmt.Errorf("pki: validate candidate trust bundle: %w", err)
	}
	bundleCA, _ := CARoot(bundleRoot)
	targetCA, _ := CARoot(targetRoot)
	bundleCAPEM, err := os.ReadFile(bundleCA)
	if err != nil {
		return fmt.Errorf("pki: read candidate CA: %w", err)
	}
	targetCAPEM, err := os.ReadFile(targetCA)
	if err != nil {
		return fmt.Errorf("pki: read active CA: %w", err)
	}
	if !bytes.Equal(bundleCAPEM, targetCAPEM) {
		return errors.New("pki: candidate CA differs from active CA; leaf renewal cannot rotate trust roots")
	}

	files := make([]installFile, 0, len(RolesForBox(hostRole))*2)
	cleanupStaged := func() {
		for _, file := range files {
			if file.Staged != "" {
				_ = os.Remove(file.Staged)
			}
		}
	}
	for _, role := range RolesForBox(hostRole) {
		bundleCert, bundleKey := LeafPaths(bundleRoot, role)
		targetCert, targetKey := LeafPaths(targetRoot, role)
		for _, pair := range [][2]string{{bundleCert, targetCert}, {bundleKey, targetKey}} {
			body, readErr := os.ReadFile(pair[0])
			if readErr != nil {
				cleanupStaged()
				return fmt.Errorf("pki: read candidate file %q: %w", pair[0], readErr)
			}
			mode, uid, gid, statErr := installMetadata(pair[0], pair[1])
			if statErr != nil {
				cleanupStaged()
				return statErr
			}
			staged, stageErr := stageInstallFile(pair[1], body, mode, uid, gid)
			if stageErr != nil {
				cleanupStaged()
				return stageErr
			}
			backup, backupErr := unusedSiblingPath(pair[1], ".pki-previous-")
			if backupErr != nil {
				cleanupStaged()
				_ = os.Remove(staged)
				return backupErr
			}
			files = append(files, installFile{Destination: pair[1], Staged: staged, Backup: backup})
		}
	}
	journal := installJournal{Phase: "applying", Files: files}
	if err := persistInstallJournal(targetRoot, journal); err != nil {
		cleanupStaged()
		return err
	}
	for index := range journal.Files {
		file := &journal.Files[index]
		if renameErr := os.Rename(file.Destination, file.Backup); renameErr != nil {
			recoveryErr := recoverInstallJournal(targetRoot)
			return errors.Join(fmt.Errorf("pki: archive active file %q: %w", file.Destination, renameErr), recoveryErr)
		}
		if err := syncDirectory(filepath.Dir(file.Destination)); err != nil {
			recoveryErr := recoverInstallJournal(targetRoot)
			return errors.Join(fmt.Errorf("pki: sync archived file %q: %w", file.Destination, err), recoveryErr)
		}
		if renameErr := os.Rename(file.Staged, file.Destination); renameErr != nil {
			recoveryErr := recoverInstallJournal(targetRoot)
			return errors.Join(fmt.Errorf("pki: activate candidate file %q: %w", file.Destination, renameErr), recoveryErr)
		}
		if err := syncDirectory(filepath.Dir(file.Destination)); err != nil {
			recoveryErr := recoverInstallJournal(targetRoot)
			return errors.Join(fmt.Errorf("pki: sync activated file %q: %w", file.Destination, err), recoveryErr)
		}
	}
	if err := ValidateTrustBundleForNode(targetRoot, hostRole, extraSANs, nodeCN); err != nil {
		recoveryErr := recoverInstallJournal(targetRoot)
		return errors.Join(fmt.Errorf("pki: validate installed trust bundle: %w", err), recoveryErr)
	}
	journal.Phase = "committed"
	if err := persistInstallJournal(targetRoot, journal); err != nil {
		return fmt.Errorf("pki: commit trust bundle journal: %w", err)
	}
	if err := recoverInstallJournal(targetRoot); err != nil {
		return fmt.Errorf("pki: clean committed trust bundle install: %w", err)
	}
	return nil
}

func persistInstallJournal(targetRoot string, journal installJournal) error {
	body, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("pki: marshal install journal: %w", err)
	}
	body = append(body, '\n')
	path := filepath.Join(targetRoot, installJournalFilename)
	if err := writeFileAtomic(path, body, 0o600, -1, -1); err != nil {
		return fmt.Errorf("pki: write install journal: %w", err)
	}
	if err := syncDirectory(targetRoot); err != nil {
		return fmt.Errorf("pki: sync install journal: %w", err)
	}
	return nil
}

func recoverInstallJournal(targetRoot string) error {
	journalPath := filepath.Join(targetRoot, installJournalFilename)
	body, err := os.ReadFile(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal installJournal
	if err := json.Unmarshal(body, &journal); err != nil {
		return fmt.Errorf("decode %s: %w", installJournalFilename, err)
	}
	if journal.Phase != "applying" && journal.Phase != "committed" {
		return fmt.Errorf("invalid install journal phase %q", journal.Phase)
	}
	resolvedRoot, err := filepath.Abs(targetRoot)
	if err != nil {
		return err
	}
	for _, file := range journal.Files {
		for _, path := range []string{file.Destination, file.Staged, file.Backup} {
			absolute, absErr := filepath.Abs(path)
			if absErr != nil || !samePathOrDescendant(absolute, resolvedRoot) {
				return fmt.Errorf("install journal path %q escapes target root", path)
			}
		}
	}
	var recoveryErrors []error
	if journal.Phase == "applying" {
		for index := len(journal.Files) - 1; index >= 0; index-- {
			file := journal.Files[index]
			if _, statErr := os.Lstat(file.Backup); statErr == nil {
				if removeErr := os.Remove(file.Destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					recoveryErrors = append(recoveryErrors, removeErr)
					continue
				}
				if renameErr := os.Rename(file.Backup, file.Destination); renameErr != nil {
					recoveryErrors = append(recoveryErrors, renameErr)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				recoveryErrors = append(recoveryErrors, statErr)
			}
		}
	}
	for _, file := range journal.Files {
		for _, path := range []string{file.Staged, file.Backup} {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				recoveryErrors = append(recoveryErrors, removeErr)
			}
		}
	}
	if len(recoveryErrors) > 0 {
		return errors.Join(recoveryErrors...)
	}
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(targetRoot)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func installMetadata(source, destination string) (os.FileMode, int, int, error) {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("pki: stat candidate file %q: %w", source, err)
	}
	mode, uid, gid := sourceInfo.Mode().Perm(), -1, -1
	if current, currentErr := os.Stat(destination); currentErr == nil {
		mode = current.Mode().Perm()
		if raw, ok := current.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(raw.Uid), int(raw.Gid)
		}
	} else if !errors.Is(currentErr, os.ErrNotExist) {
		return 0, 0, 0, fmt.Errorf("pki: stat active file %q: %w", destination, currentErr)
	}
	return mode, uid, gid, nil
}

func stageInstallFile(destination string, body []byte, mode os.FileMode, uid, gid int) (string, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", fmt.Errorf("pki: create active leaf directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".pki-renew-")
	if err != nil {
		return "", fmt.Errorf("pki: create staged file for %q: %w", destination, err)
	}
	path := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return "", fmt.Errorf("pki: write staged file for %q: %w", destination, err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("pki: sync staged file for %q: %w", destination, err)
	}
	if err := file.Chmod(mode); err != nil {
		return "", fmt.Errorf("pki: chmod staged file for %q: %w", destination, err)
	}
	if uid >= 0 && gid >= 0 {
		if err := file.Chown(uid, gid); err != nil {
			return "", fmt.Errorf("pki: chown staged file for %q: %w", destination, err)
		}
		if err := file.Chmod(mode); err != nil {
			return "", fmt.Errorf("pki: restore mode on staged file for %q: %w", destination, err)
		}
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("pki: close staged file for %q: %w", destination, err)
	}
	ok = true
	return path, nil
}

func unusedSiblingPath(destination, pattern string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(destination), pattern)
	if err != nil {
		return "", fmt.Errorf("pki: reserve backup path for %q: %w", destination, err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("pki: close backup placeholder for %q: %w", destination, err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("pki: clear backup placeholder for %q: %w", destination, err)
	}
	return path, nil
}

func writeFileAtomic(path string, body []byte, mode os.FileMode, uid, gid int) error {
	staged, err := stageInstallFile(path, body, mode, uid, gid)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, path); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

func validateTrustBundle(rootDir, hostRole string, extraSANs AltNames, nodeCN string) error {
	roles := RolesForBox(hostRole)
	if len(roles) == 0 {
		return fmt.Errorf("pki: no roles for host role %q", hostRole)
	}

	caCertPath, _ := CARoot(rootDir)
	caCert, err := loadPublicCertificate(caCertPath, "CA")
	if err != nil {
		return err
	}
	now := time.Now()
	if !caCert.IsCA {
		return fmt.Errorf("pki: CA certificate %q is not marked as a CA", caCertPath)
	}
	if now.Before(caCert.NotBefore) || !now.Before(caCert.NotAfter) {
		return fmt.Errorf("pki: CA certificate %q is outside its validity window", caCertPath)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	for _, role := range roles {
		certPath, keyPath := LeafPaths(rootDir, role)
		cert, err := loadExistingLeaf(certPath, keyPath)
		if err != nil {
			return fmt.Errorf("pki: validate %s/%s: %w", role.Directory, role.Filename, err)
		}
		if cert == nil {
			return fmt.Errorf("pki: trust bundle missing %s", certPath)
		}
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return fmt.Errorf("pki: leaf %q is outside its validity window", certPath)
		}
		expectedCN := role.CommonName
		if nodeCN != "" && hostRole == "compute-only" && RoleUsesNodeIdentity(role) {
			expectedCN = nodeCN
		}
		if cert.Subject.CommonName != expectedCN {
			return fmt.Errorf("pki: leaf %q has CN %q; want %q", certPath, cert.Subject.CommonName, expectedCN)
		}
		requiredSANs := mergeAltNames(role.AltNames, extraSANs)
		if !certificateHasSANs(cert, requiredSANs) {
			return fmt.Errorf("pki: leaf %q is missing one or more required SANs", certPath)
		}
		keyUsage := x509.ExtKeyUsageClientAuth
		if role.Kind == KindServer {
			keyUsage = x509.ExtKeyUsageServerAuth
		}
		verifyOptions := x509.VerifyOptions{
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{keyUsage},
		}
		if _, err := cert.Verify(verifyOptions); err != nil {
			return fmt.Errorf("pki: leaf %q does not verify against CA: %w", certPath, err)
		}
	}
	return nil
}

// ValidateIssuanceMaterial checks that rootDir is an operator-side PKI root
// capable of issuing leaves. It does not create or rotate anything and it
// permits missing leaves because PrepareTrustBundle may issue those leaves
// for a newly-added endpoint. The CA certificate and private key must both
// already exist and match.
func ValidateIssuanceMaterial(rootDir, hostRole string) error {
	if len(RolesForBox(hostRole)) == 0 {
		return fmt.Errorf("pki: no roles for host role %q", hostRole)
	}
	caCertPath, caKeyPath := CARoot(rootDir)
	caCert, caKey, err := loadExistingCA(caCertPath, caKeyPath)
	if err != nil {
		return fmt.Errorf("pki: validate issuance CA: %w", err)
	}
	if caCert == nil || caKey == nil || !caCert.IsCA {
		return fmt.Errorf("pki: issuance CA is incomplete or not a CA")
	}
	if err := caCert.CheckSignatureFrom(caCert); err != nil {
		return fmt.Errorf("pki: issuance CA is not self-signed: %w", err)
	}
	if time.Now().After(caCert.NotAfter) {
		return fmt.Errorf("pki: issuance CA is expired")
	}
	for _, role := range RolesForBox(hostRole) {
		certPath, keyPath := LeafPaths(rootDir, role)
		certExists := fileExists(certPath)
		keyExists := fileExists(keyPath)
		if !certExists && !keyExists {
			continue
		}
		if !certExists || !keyExists {
			return fmt.Errorf("pki: issuance leaf %s/%s has only one half", role.Directory, role.Filename)
		}
		if _, err := loadExistingLeaf(certPath, keyPath); err != nil {
			return fmt.Errorf("pki: validate issuance leaf %s/%s: %w", role.Directory, role.Filename, err)
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadPublicCertificate(path, label string) (*x509.Certificate, error) {
	if err := enforceCertMode(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("pki: trust bundle missing %s certificate %q: %w", label, path, err)
		}
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pki: read %s certificate %q: %w", label, path, err)
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("pki: %s certificate %q is not PEM-encoded", label, path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: parse %s certificate %q: %w", label, path, err)
	}
	return cert, nil
}
