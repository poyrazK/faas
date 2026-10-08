package main

// adr: 775
import (
	"context"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

func cmdGlobalQuotaDelivery(verb string, args []string) int {
	if len(args) == 0 || !globaldirectory.ValidSlug(args[0]) {
		return printErr("Invalid global app", fmt.Errorf("a canonical global slug is required"))
	}
	fs := newFlagSet("global-app "+verb, flag.ContinueOnError)
	allocation := fs.String("allocation", "", "original allocation UUID (required)")
	id := fs.String("delivery", "", "stable delivery UUID (required)")
	var region, purpose, consent string
	if verb == "queue-quota-delivery" {
		fs.StringVar(&region, "region", "", "original selected region (required)")
		fs.StringVar(&purpose, "purpose", "grant", "grant or retirement")
		fs.StringVar(&consent, "runtime-consent", "", "original runtime consent UUID (required for grant)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || !globaldirectory.ValidID(*allocation) || !globaldirectory.ValidID(*id) {
		return printErr("Invalid delivery", fmt.Errorf("--allocation and --delivery must be canonical nonzero UUIDs"))
	}
	if verb == "queue-quota-delivery" && (region == "" || !api.ValidComputeRegion(region) || (purpose != "grant" && purpose != "retirement") || (purpose == "grant" && !globaldirectory.ValidID(consent)) || (purpose == "retirement" && consent != "")) {
		return printErr("Invalid delivery", fmt.Errorf("supply --region and grant with --runtime-consent, or --purpose retirement without consent"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.GlobalQuotaDeliveryResponse
	switch verb {
	case "queue-quota-delivery":
		out, err = c.QueueGlobalQuotaDelivery(context.Background(), args[0], *allocation, *id, api.QueueGlobalQuotaDeliveryRequest{Region: region, Purpose: purpose, ConsentID: consent})
	case "show-quota-delivery":
		out, err = c.GetGlobalQuotaDelivery(context.Background(), args[0], *allocation, *id)
	case "retry-quota-delivery":
		out, err = c.RetryGlobalQuotaDelivery(context.Background(), args[0], *allocation, *id)
	default:
		return 1
	}
	if err != nil {
		return printErr("Could not access quota delivery", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\tattempts=%d\tcode=%s\n", out.ID, out.Purpose, out.Status, out.Attempts, out.LastCode)
	return 0
}
