package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/sealsig"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchRuntimeSecret = "runtime-secret"

// defaultRuntimeSigningKey is where the seal_domains role installs apid's
// runtime secret signing key.
const defaultRuntimeSigningKey = "/etc/faas/secrets/domains/runtime-signing.key"

func cmdRuntimeSecret(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl runtime-secret <signing-keygen|sign-existing> [flags]", dispatchRuntimeSecret)
		return 0
	}
	var err error
	switch args[0] {
	case "signing-keygen":
		err = runRuntimeSecretSigningKeygen(args[1:])
	case "sign-existing":
		err = runRuntimeSecretSignExisting(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl runtime-secret: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl runtime-secret %s: %v\n", args[0], err)
		return 3
	}
	return 0
}

// runRuntimeSecretSigningKeygen writes apid's runtime secret signing key
// pair (ADR 20261028161502, amendment 1). It runs offline, on the
// operator's machine; the seal_domains role installs the private key for
// apid and the public key for vmmd.
func runRuntimeSecretSigningKeygen(args []string) error {
	fs := flag.NewFlagSet("runtime-secret signing-keygen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	signKey := fs.String("sign-key", "", "new private key path (PKCS#8 PEM, written 0600)")
	verifyKey := fs.String("verify-key", "", "new public key path (PKIX PEM)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *signKey == "" || *verifyKey == "" {
		return errors.New("--sign-key and --verify-key are required")
	}
	if err := sealsig.WriteSigningKeyPair(*signKey, *verifyKey); err != nil {
		return err
	}
	PrintOK(osStdout, "Runtime secret signing key written to %s; verify key to %s\n", *signKey, *verifyKey)
	return nil
}

// runtimeSignStore is what the backfill reads and writes; *state.PgStore
// implements it.
type runtimeSignStore interface {
	ListSecretBindingDocuments(ctx context.Context, after string, limit int) ([]state.SecretBindingDocument, error)
	sealsig.RuntimeStore
}

// runtimeSignReport is what the backfill did.
type runtimeSignReport struct {
	// Envelopes is every distinct stored runtime envelope.
	Envelopes int `json:"envelopes"`
	// Signed envelopes had no signature and now have one.
	Signed int `json:"signed"`
	// AlreadySigned envelopes carry a signature by this key.
	AlreadySigned int `json:"already_signed"`
	// Resigned envelopes carried a trusted previous key's signature and now
	// carry this key's (a signing-key rotation).
	Resigned int `json:"resigned"`
	// Invalid envelopes carry a signature neither this key nor a trusted one
	// made. They are left as they are: vmmd refuses them, and an operator
	// must look.
	Invalid int `json:"invalid"`
	// Unparsed documents hold a payload the walk cannot read.
	Unparsed int `json:"unparsed"`
}

const (
	// runtimeSignPage is how many documents the backfill reads at a time.
	runtimeSignPage = 100
	// runtimeSignLookup is how many signatures it looks up at a time.
	runtimeSignLookup = 1024
)

// signExistingRuntime signs every stored runtime envelope that has no
// signature, as it stands (ADR 20261028161502 amendment 1, backfill: trust
// on first use), and signs again each envelope a trusted previous key
// signed (a signing-key rotation; trusted may be nil). It walks the
// release-secret rekey documents, which cover every stored runtime secret.
func signExistingRuntime(ctx context.Context, store runtimeSignStore, key ed25519.PrivateKey, trusted *sealsig.Verifier) (runtimeSignReport, error) {
	var report runtimeSignReport
	signer := sealsig.NewSigner(key)
	seen := map[string]bool{}
	after := ""
	for {
		docs, err := store.ListSecretBindingDocuments(ctx, after, runtimeSignPage)
		if err != nil {
			return report, fmt.Errorf("list runtime secret documents: %w", err)
		}
		var blobs [][]byte
		for _, doc := range docs {
			if doc.Cursor() <= after {
				return report, errors.New("runtime secret documents listed out of order")
			}
			after = doc.Cursor()
			_, err := state.TransformSecretRekeyDocument(doc.SecretRekeyDocument, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
				if digest := string(sealsig.RuntimeDigest(e.Ciphertext)); !seen[digest] {
					seen[digest] = true
					blobs = append(blobs, e.Ciphertext)
				}
				return e, nil
			})
			if err != nil {
				report.Unparsed++
			}
		}
		for start := 0; start < len(blobs); start += runtimeSignLookup {
			if err := signRuntimeBlobs(ctx, store, signer, trusted, blobs[start:min(start+runtimeSignLookup, len(blobs))], &report); err != nil {
				return report, err
			}
		}
		if len(docs) < runtimeSignPage {
			return report, nil
		}
	}
}

func signRuntimeBlobs(ctx context.Context, store runtimeSignStore, signer *sealsig.Signer, trusted *sealsig.Verifier, blobs [][]byte, report *runtimeSignReport) error {
	if len(blobs) == 0 {
		return nil
	}
	digests := make([][]byte, len(blobs))
	for i, blob := range blobs {
		digests[i] = sealsig.RuntimeDigest(blob)
	}
	found, err := store.RuntimeSecretSignatures(ctx, digests)
	if err != nil {
		return fmt.Errorf("read runtime secret signatures: %w", err)
	}
	for i, blob := range blobs {
		report.Envelopes++
		msg := sealsig.RuntimeSecretMessage(blob)
		// Ed25519 is deterministic: this key's signature of the envelope is
		// the one stored, if any.
		sig := signer.Sign(msg)
		stored, signed := found[string(digests[i])]
		switch {
		case bytes.Equal(stored, sig):
			report.AlreadySigned++
			continue
		case signed && (trusted == nil || trusted.Check(msg, stored) != nil):
			report.Invalid++
			continue
		}
		if err := store.PutRuntimeSecretSignature(ctx, digests[i], sig); err != nil {
			return fmt.Errorf("store runtime secret signature: %w", err)
		}
		if signed {
			report.Resigned++
		} else {
			report.Signed++
		}
	}
	return nil
}

func runRuntimeSecretSignExisting(args []string) error {
	fs := flag.NewFlagSet("runtime-secret sign-existing", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	signKey := fs.String("sign-key", defaultRuntimeSigningKey, "apid's runtime secret signing key")
	trustedKeys := fs.String("trusted-keys", "", "public keys of previous runtime signing keys; envelopes they signed are signed again with --sign-key")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: FAAS_PG_DSN, DATABASE_URL, or deploy env)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key, err := sealsig.LoadSigningKey(*signKey)
	if err != nil {
		return err
	}
	var trusted *sealsig.Verifier
	if *trustedKeys != "" {
		keys, err := sealsig.LoadVerifyKeys(*trustedKeys)
		if err != nil {
			return err
		}
		trusted = sealsig.NewVerifier(keys, true)
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	report, err := signExistingRuntime(ctx, state.NewPgStore(pool), key, trusted)
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		if err := json.NewEncoder(osStdout).Encode(report); err != nil {
			return err
		}
	} else {
		PrintOK(osStdout, "Runtime secrets: %d envelopes, %d signed, %d signed again, %d already signed, %d signed by an untrusted key, %d unreadable documents\n",
			report.Envelopes, report.Signed, report.Resigned, report.AlreadySigned, report.Invalid, report.Unparsed)
	}
	if report.Invalid+report.Unparsed > 0 {
		return errors.New("some runtime secrets are not signed by this key; review them, or pass the previous key's public key as --trusted-keys, before turning on FAAS_RUNTIME_SECRET_SIGNATURE_REQUIRED")
	}
	return nil
}

// installResealSigner makes the runtime envelopes an operator re-seal
// writes carry apid's signature (ADR 20261028161502, amendment 1). With no
// key the re-sealed envelopes are unsigned until sign-existing runs.
func installResealSigner(ctx context.Context, path string, store sealsig.RuntimeStore) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	key, err := sealsig.LoadSigningKey(path)
	if err != nil {
		return nil, err
	}
	secretbox.SetRuntimeSealHook(sealsig.RuntimeSealHook(ctx, store, sealsig.NewSigner(key)))
	return func() { secretbox.SetRuntimeSealHook(nil) }, nil
}
