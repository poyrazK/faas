package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

const dispatchLiveQuery = "live-query"

// defaultLiveSigningKey is where livequeryd_service installs apid's live
// signing key.
const defaultLiveSigningKey = "/etc/faas/secrets/livequeryd/signing.key"

func cmdLiveQuery(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl live-query <signing-keygen|sign-existing> [flags]", dispatchLiveQuery)
		return 0
	}
	var err error
	switch args[0] {
	case "signing-keygen":
		err = runLiveQuerySigningKeygen(args[1:])
	case "sign-existing":
		err = runLiveQuerySignExisting(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl live-query: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl live-query %s: %v\n", args[0], err)
		return 3
	}
	return 0
}

// runLiveQuerySigningKeygen writes apid's live signing key pair (ADR
// 20261028161502). It runs offline, on the operator's machine; the
// livequeryd_service role installs the private key for apid and the public
// key for livequeryd.
func runLiveQuerySigningKeygen(args []string) error {
	fs := flag.NewFlagSet("live-query signing-keygen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	signKey := fs.String("sign-key", "", "new private key path (PKCS#8 PEM, written 0600)")
	verifyKey := fs.String("verify-key", "", "new public key path (PKIX PEM)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *signKey == "" || *verifyKey == "" {
		return errors.New("--sign-key and --verify-key are required")
	}
	if err := livekeys.WriteSigningKeyPair(*signKey, *verifyKey); err != nil {
		return err
	}
	PrintOK(osStdout, "Live signing key written to %s; verify key to %s\n", *signKey, *verifyKey)
	return nil
}

// liveSignStore is what the backfill reads and writes; *livekeys.Store is
// one.
type liveSignStore interface {
	SealedKeysAfter(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	SignKey(ctx context.Context, key livekeys.Sealed, sig []byte) (bool, error)
	SealedLoginsAfter(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	SignLogin(ctx context.Context, login livekeys.Sealed, sig []byte) (bool, error)
}

// liveSignCounts is what the backfill did with one kind of row.
type liveSignCounts struct {
	// Signed rows were unsigned and now carry a signature.
	Signed int `json:"signed"`
	// AlreadySigned rows carried this key's signature.
	AlreadySigned int `json:"already_signed"`
	// Resigned rows carried a trusted previous key's signature and now
	// carry this key's (a signing-key rotation).
	Resigned int `json:"resigned"`
	// Invalid rows carry a signature neither this key nor a trusted one
	// made. They are left as they are: livequeryd refuses them.
	Invalid int `json:"invalid"`
	// Changed rows were rewritten while the backfill ran; run it again.
	Changed int `json:"changed"`
}

type liveSignReport struct {
	Keys   liveSignCounts `json:"keys"`
	Logins liveSignCounts `json:"logins"`
}

// liveSignPage is how many rows the backfill reads at a time.
const liveSignPage = 100

// signExistingLive signs every unsigned live key and login as it stands
// (ADR 20261028161502, backfill: trust on first use), and signs again each
// row whose signature a trusted previous key made (a signing-key rotation;
// trusted may be nil). Each signature names the row it is stored on, and
// each write is a compare-and-swap on the ciphertext, recipient and
// signature the backfill read.
func signExistingLive(ctx context.Context, store liveSignStore, signer *livekeys.Signer, trusted *livekeys.Verifier) (liveSignReport, error) {
	var report liveSignReport
	kinds := []struct {
		kind   string
		page   func(context.Context, string, int) ([]livekeys.Sealed, error)
		sign   func(context.Context, livekeys.Sealed, []byte) (bool, error)
		counts *liveSignCounts
		kid    func(livekeys.Sealed) string
	}{
		{livekeys.SignatureKindKey, store.SealedKeysAfter, store.SignKey, &report.Keys, func(r livekeys.Sealed) string { return r.ID }},
		{livekeys.SignatureKindLogin, store.SealedLoginsAfter, store.SignLogin, &report.Logins, func(livekeys.Sealed) string { return "" }},
	}
	for _, k := range kinds {
		for after := ""; ; {
			rows, err := k.page(ctx, after, liveSignPage)
			if err != nil {
				return report, err
			}
			for _, row := range rows {
				// Ed25519 is deterministic: this key's signature of the row
				// is the one it already carries, if any.
				sig := signer.Sign(k.kind, row.DatabaseID, k.kid(row), row.Recipient, row.Blob)
				resign := len(row.Signature) > 0
				switch {
				case bytes.Equal(row.Signature, sig):
					k.counts.AlreadySigned++
					continue
				case resign && (trusted == nil || trusted.Check(k.kind, row.DatabaseID, k.kid(row), row.Recipient, row.Blob, row.Signature) != nil):
					k.counts.Invalid++
					continue
				}
				signed, err := k.sign(ctx, row, sig)
				if err != nil {
					return report, err
				}
				switch {
				case !signed:
					k.counts.Changed++
				case resign:
					k.counts.Resigned++
				default:
					k.counts.Signed++
				}
			}
			if len(rows) < liveSignPage {
				break
			}
			after = rows[len(rows)-1].ID
		}
	}
	return report, nil
}

func runLiveQuerySignExisting(args []string) error {
	fs := flag.NewFlagSet("live-query sign-existing", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	signKey := fs.String("sign-key", defaultLiveSigningKey, "apid's live signing key")
	trustedKeys := fs.String("trusted-keys", "", "public keys of previous live signing keys; rows they signed are signed again with --sign-key")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: FAAS_PG_DSN, DATABASE_URL, or deploy env)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key, err := livekeys.LoadSigningKey(*signKey)
	if err != nil {
		return err
	}
	var trusted *livekeys.Verifier
	if *trustedKeys != "" {
		keys, err := livekeys.LoadVerifyKeys(*trustedKeys)
		if err != nil {
			return err
		}
		trusted = livekeys.NewVerifier(keys, true)
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report, err := signExistingLive(ctx, livekeys.New(pool), livekeys.NewSigner(key), trusted)
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		if err := json.NewEncoder(osStdout).Encode(report); err != nil {
			return err
		}
	} else {
		for _, kind := range []struct {
			name   string
			counts liveSignCounts
		}{{"Live keys", report.Keys}, {"Live logins", report.Logins}} {
			c := kind.counts
			PrintOK(osStdout, "%s: %d signed, %d signed again, %d already signed, %d signed by an untrusted key, %d changed meanwhile\n",
				kind.name, c.Signed, c.Resigned, c.AlreadySigned, c.Invalid, c.Changed)
		}
	}
	if report.Keys.Invalid+report.Logins.Invalid > 0 {
		return errors.New("some rows carry a signature neither --sign-key nor --trusted-keys made; review them, or pass the previous key's public key as --trusted-keys")
	}
	if report.Keys.Changed+report.Logins.Changed > 0 {
		return errors.New("rows changed while signing; run sign-existing again")
	}
	return nil
}
