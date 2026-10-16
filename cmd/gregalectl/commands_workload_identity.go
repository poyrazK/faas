// commands_workload_identity.go — operator-side provisioning for the cluster
// workload-identity signing key (ADR-1141). One RSA key signs the workload
// tokens every vmmd mints for app instances; its public JWKS is what apid
// (queue leases, Flags, Operations), outboundd and cloud providers trust.
//
// The leaves write into a local directory the operator then distributes:
// set the Ansible variable faas_workload_identity_source_dir to it and the
// vmmd_service role installs the key and key id on compute nodes while the
// control_plane_service role installs the JWKS for apid.
//
//   - init   — write workload-identity.key (0400), workload-identity.kid
//     (0444) and workload-identity.jwks.json (0444); refuses to
//     overwrite an existing key
//   - rotate — write a new key and key id; the JWKS keeps the current
//     public key so tokens it signed verify until they expire.
//     Roll the JWKS out to the control plane before the key.
//   - status — print the key id, key size and the JWKS key ids
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-jose/go-jose/v4"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const dispatchWorkloadIdentity = "workload-identity"

const (
	workloadIdentityKeyFile  = "workload-identity.key"
	workloadIdentityKIDFile  = "workload-identity.kid"
	workloadIdentityJWKSFile = "workload-identity.jwks.json"
	// 3072-bit RSA: RS256 is what cloud OIDC federation accepts (ADR-1141).
	workloadIdentityKeyBits = 3072
)

func cmdWorkloadIdentity(args []string) int {
	parent, _ := lookupCliCommand(dispatchWorkloadIdentity)
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregalectl workload-identity <init|rotate|status> --out DIR", dispatchWorkloadIdentity)
		return 1
	}
	switch args[0] {
	case subInit, subRotate:
		return cmdWorkloadIdentityWrite(args[0], args[1:], os.Stdout, os.Stderr)
	case subStatus:
		return cmdWorkloadIdentityStatus(args[1:], os.Stdout, os.Stderr)
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregalectl workload-identity: unknown subcommand %q (known: init, rotate, status)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

func workloadIdentityFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	out := fs.String("out", "", "directory holding workload-identity.{key,kid,jwks.json} (required)")
	return fs, out
}

func cmdWorkloadIdentityWrite(sub string, args []string, stdout, stderr io.Writer) int {
	fs, out := workloadIdentityFlags("workload-identity " + sub)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *out == "" {
		_, _ = fmt.Fprintf(stderr, "usage: gregalectl workload-identity %s --out DIR\n", sub)
		return 1
	}
	kid, kept, err := writeWorkloadIdentity(*out, sub == subRotate)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl workload-identity %s: %v\n", sub, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "workload identity key id: %s\n", kid)
	if kept != "" {
		_, _ = fmt.Fprintf(stdout, "kept previous key id in the JWKS: %s (install the JWKS on the control plane before the key on compute nodes)\n", kept)
	}
	return 0
}

// writeWorkloadIdentity generates a key in dir. A rotation requires an
// existing key and keeps its public half in the JWKS; init refuses one.
func writeWorkloadIdentity(dir string, rotate bool) (kid, kept string, err error) {
	keyPath := filepath.Join(dir, workloadIdentityKeyFile)
	var previous *jose.JSONWebKey
	switch existing, readErr := os.ReadFile(keyPath); {
	case readErr == nil && !rotate:
		return "", "", fmt.Errorf("%s already exists; use rotate", keyPath)
	case readErr == nil:
		key, parseErr := workloadidentity.ParseRSAPrivateKeyPEM(existing)
		if parseErr != nil {
			return "", "", parseErr
		}
		prev, jwkErr := workloadidentity.PublicJWK(&key.PublicKey)
		if jwkErr != nil {
			return "", "", jwkErr
		}
		previous = &prev
	case !errors.Is(readErr, os.ErrNotExist):
		return "", "", readErr
	case rotate:
		return "", "", fmt.Errorf("%s does not exist; use init", keyPath)
	}
	key, err := rsa.GenerateKey(rand.Reader, workloadIdentityKeyBits)
	if err != nil {
		return "", "", fmt.Errorf("generate RSA key: %w", err)
	}
	current, err := workloadidentity.PublicJWK(&key.PublicKey)
	if err != nil {
		return "", "", err
	}
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{current}}
	if previous != nil {
		set.Keys = append(set.Keys, *previous)
		kept = previous.KeyID
	}
	// The JWKS must be one apid accepts before anything is written.
	if _, err := workloadidentity.NewVerifier(set, workloadidentity.DefaultIssuer); err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	jwks, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	for _, f := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{workloadIdentityJWKSFile, append(jwks, '\n'), 0o444},
		{workloadIdentityKIDFile, []byte(current.KeyID + "\n"), 0o444},
		{workloadIdentityKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o400},
	} {
		if err := writeWorkloadIdentityFile(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return "", "", err
		}
	}
	return current.KeyID, kept, nil
}

func writeWorkloadIdentityFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func cmdWorkloadIdentityStatus(args []string, stdout, stderr io.Writer) int {
	fs, out := workloadIdentityFlags("workload-identity status")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *out == "" {
		_, _ = fmt.Fprintln(stderr, "usage: gregalectl workload-identity status --out DIR")
		return 1
	}
	raw, err := os.ReadFile(filepath.Join(*out, workloadIdentityKeyFile))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl workload-identity status: %v\n", err)
		return 1
	}
	key, err := workloadidentity.ParseRSAPrivateKeyPEM(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl workload-identity status: %v\n", err)
		return 1
	}
	jwk, err := workloadidentity.PublicJWK(&key.PublicKey)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl workload-identity status: %v\n", err)
		return 1
	}
	kidFile, _ := os.ReadFile(filepath.Join(*out, workloadIdentityKIDFile))
	var set jose.JSONWebKeySet
	jwksRaw, _ := os.ReadFile(filepath.Join(*out, workloadIdentityJWKSFile))
	_ = json.Unmarshal(jwksRaw, &set)
	ids := make([]string, 0, len(set.Keys))
	for _, k := range set.Keys {
		ids = append(ids, k.KeyID)
	}
	_, _ = fmt.Fprintf(stdout, "key id: %s (RSA %d)\nkid file: %s\njwks key ids: %s\n",
		jwk.KeyID, key.N.BitLen(), strings.TrimSpace(string(kidFile)), strings.Join(ids, ", "))
	if strings.TrimSpace(string(kidFile)) != jwk.KeyID || len(set.Keys) == 0 || set.Keys[0].KeyID != jwk.KeyID {
		_, _ = fmt.Fprintln(stderr, "gregalectl workload-identity status: the kid file or JWKS does not match the key")
		return 1
	}
	return 0
}
