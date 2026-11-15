// adr: 20261112223000

package main

import (
	"flag"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// ParseAppSettingFlags runs the `gregale app <slug>` setting flags that close
// CLI parity gaps (ADR-20261112223000 §3) on args and returns the request.
func ParseAppSettingFlags(args []string) (api.UpdateAppRequest, error) {
	fs := newFlagSet("app", flag.ContinueOnError, io.Discard)
	flags := registerAppSettingFlags(fs)
	if err := fs.Parse(args); err != nil {
		return api.UpdateAppRequest{}, err
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	var req api.UpdateAppRequest
	err := flags.apply(explicit, &req)
	return req, err
}
