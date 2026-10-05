package main

// adr: 632
import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func cmdRuntimeQuotaKeys(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gregalectl runtime-quota-keys <init|status|trust> [flags]")
		return 1
	}
	if args[0] == "trust" {
		return cmdRuntimeQuotaTrust(args[1:])
	}
	if args[0] != subInit && args[0] != subStatus {
		fmt.Fprintln(os.Stderr, "runtime-quota-keys: use init, status, or trust")
		return 1
	}
	fs := flag.NewFlagSet("runtime-quota-keys "+args[0], flag.ContinueOnError)
	issuer := fs.String("issuer", "", "immutable nonzero cell/directory UUID")
	role := fs.String("role", "", "cell or directory")
	dir := fs.String("dir", "", "absolute key directory; init refuses existing directories")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*dir) || *issuer == "" || (*role != runtimequota.Cell && *role != runtimequota.Directory) {
		fmt.Fprintln(os.Stderr, "runtime-quota-keys: --issuer, --role=cell|directory and absolute --dir are required")
		return 1
	}
	if args[0] == subInit {
		if _, err := runtimequota.ProvisionAuthorityKeys(*dir, *role, *issuer); err != nil {
			fmt.Fprintln(os.Stderr, "runtime-quota-keys:", err)
			return 1
		}
	}
	cfg := runtimequota.KeyConfig{PrivateKeyPath: filepath.Join(*dir, runtimequota.PrivateKeyFilename), TrustBundlePath: filepath.Join(*dir, runtimequota.TrustFilename)}
	keys, err := runtimequota.LoadAuthorityKeys(cfg, *role, *issuer)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-quota-keys:", err)
		return 1
	}
	if _, err := fmt.Fprintf(osStdout, "role=%s issuer=%s key_id=%s\nprivate_key_path=%s\ntrust_bundle_path=%s\n", *role, keys.Signer.IssuerID(), keys.Signer.KeyID(), cfg.PrivateKeyPath, cfg.TrustBundlePath); err != nil {
		fmt.Fprintln(os.Stderr, "runtime-quota-keys: material is retained; status output failed:", err)
		return 1
	}
	return 0
}

type quotaTrustInputs []string

func (v *quotaTrustInputs) String() string { return "public trust bundle paths" }
func (v *quotaTrustInputs) Set(s string) error {
	if len(*v) >= api.RegionalRuntimeTrustAnchorsMax {
		return runtimequota.ErrInvalid
	}
	*v = append(*v, s)
	return nil
}

func cmdRuntimeQuotaTrust(args []string) int {
	fs := flag.NewFlagSet("runtime-quota-keys trust", flag.ContinueOnError)
	var inputs quotaTrustInputs
	fs.Var(&inputs, "input", "absolute public bundle path (repeat for overlapping rotation keys)")
	out := fs.String("out", "", "absolute new public bundle path; refuses overwrite")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 || len(inputs) == 0 || !filepath.IsAbs(*out) {
		fmt.Fprintln(os.Stderr, "runtime-quota-keys trust: --input and absolute --out are required")
		return 1
	}
	var anchors []runtimequota.Anchor
	for _, path := range inputs {
		rows, err := runtimequota.LoadTrustAnchors(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "runtime-quota-keys trust:", err)
			return 1
		}
		anchors = append(anchors, rows...)
		if len(anchors) > api.RegionalRuntimeTrustAnchorsMax {
			fmt.Fprintln(os.Stderr, "runtime-quota-keys trust:", runtimequota.ErrInvalid)
			return 1
		}
	}
	if err := runtimequota.WriteTrustBundle(*out, anchors); err != nil {
		fmt.Fprintln(os.Stderr, "runtime-quota-keys trust:", err)
		return 1
	}
	if _, err := fmt.Fprintf(osStdout, "trust_bundle_path=%s anchors=%d\n", *out, len(anchors)); err != nil {
		fmt.Fprintln(os.Stderr, "runtime-quota-keys trust: bundle is retained; status output failed:", err)
		return 1
	}
	return 0
}
