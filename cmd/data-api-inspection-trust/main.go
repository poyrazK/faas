// data-api-inspection-trust prints the canonical fingerprint of a public CA
// bundle used by the hosted Data API inspection task.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: data-api-inspection-trust CA_BUNDLE.pem")
		os.Exit(2)
	}
	// The operator supplies a path to a public CA bundle on the host.
	f, err := os.Open(os.Args[1]) //nolint:forbidigo // Operator-owned trust material is intentionally read by path.
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open CA bundle")
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, dataapiinspectioncommand.MaxTrustBundleBytes+1))
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read CA bundle")
		os.Exit(1)
	}
	fingerprint, err := dataapiinspectioncommand.TrustBundleFingerprint(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid CA bundle")
		os.Exit(1)
	}
	fmt.Println(fingerprint)
}
