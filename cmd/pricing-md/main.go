// Command pricing-md generates the customer plan page from the authoritative
// api.LimitsFor table. Keeping the renderer in a small pure-Go command makes
// pricing reviewable and lets CI reject hand-edited plan numbers.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pricing-md:", err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	_, err := io.WriteString(w, render())
	return err
}

// render is deterministic: api.Plans is the canonical order and every value
// is read from api.LimitsFor. PriceMillicents is converted without floating
// point arithmetic so the generated page cannot round a price incorrectly.
func render() string {
	var b strings.Builder
	b.WriteString("# Plans and pricing\n\n")
	b.WriteString("<!-- GENERATED — do not edit by hand; regenerate with `make pricing-md`. -->\n\n")
	b.WriteString("Gregale pricing and quotas come from [`pkg/api/limits.go`](../pkg/api/limits.go). The same table is enforced by the API, so this page is generated rather than maintained separately. Prices are monthly and shown in euros. Usage beyond the included GB-RAM-hours is billed at €0.01 per GB-RAM-hour on paid plans.\n\n")
	b.WriteString("## At a glance\n\n")
	b.WriteString("| Plan | Monthly | Deployed apps | PR preview apps | Developer apps | Concurrent instances | RAM / app | Included GB-RAM-hours | App layer | Idle timeout |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, plan := range api.Plans {
		l, ok := api.LimitsFor(plan)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| **%s** | %s | %d | %d | %d | %d | %d MB | %d | %d MB | %s |\n",
			titlePlan(plan), formatPrice(l.PriceMillicents), l.DeployedApps,
			l.PreviewApps, l.DeveloperApps, l.MaxConcurrency, l.RAMMB, l.IncludedGBHours,
			l.AppLayerMaxMB,
			formatDuration(l.IdleTimeoutS))
	}
	b.WriteString("\n## What each limit means\n\n")
	b.WriteString("- **Deployed apps** is the maximum number of production app records on the plan. `gregale dev` environments have a separate developer-app allowance.\n")
	b.WriteString("- **PR preview apps** is the separate limit for temporary pull-request workloads. Each preview dependency uses a slot; the whole set is reserved together.\n")
	b.WriteString("- **Concurrent instances** is the per-app wake/instance ceiling; request concurrency inside one VM is separately bounded by the plan.\n")
	b.WriteString("- **RAM / app** and **app layer** are hard build/runtime ceilings. Smaller resource profiles remain available where the plan permits them.\n")
	b.WriteString("- **Included GB-RAM-hours** is the monthly compute allowance. Free stops at its allowance; paid plans can accrue overage at the published rate.\n")
	b.WriteString("- **Idle timeout** is when an inactive app is parked. A later request wakes it from its snapshot; see [scale-to-zero](cold-wake.md).")
	fmt.Fprintf(&b, " Functions default to %s on every plan; any app or function can set its own value from %s up to twice the plan default.\n\n",
		formatDuration(api.FunctionIdleTimeoutDefaultSeconds), formatDuration(api.IdleTimeoutFloorSeconds))
	b.WriteString("## Developer environments\n\n")
	fmt.Fprintf(&b, "Each [`gregale dev`](gregale-dev.md) environment keeps a lease that every sync renews. The default lease is %s; `--ttl` or `dev.ttl` in `gregale.yaml` chooses another value from %s up to the plan maximum.\n\n",
		formatDuration(int(api.DeveloperLeaseDefault.Seconds())), formatDuration(int(api.DeveloperLeaseMin.Seconds())))
	b.WriteString("| Plan | Developer apps | Maximum lease |\n")
	b.WriteString("|---|---:|---:|\n")
	for _, plan := range api.Plans {
		l, ok := api.LimitsFor(plan)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| **%s** | %d | %s |\n", titlePlan(plan), l.DeveloperApps, formatDuration(l.DeveloperLeaseMaxHours*3600))
	}
	b.WriteString("\n## Choose a plan\n\n")
	b.WriteString("Start on **Free** for a small public API or a trial. **Hobby** unlocks the paid observability, async, and data surfaces. **Pro** is the normal production tier for teams, while **Scale** raises the app, concurrency, RAM, and usage ceilings. Feature maturity and entitlement are listed in the [capability matrix](capabilities.md).\n\n")
	b.WriteString("Plan changes are safe to preview with `gregale plan`; quota errors include the exact observed value, limit, and next action.\n")
	return b.String()
}

func titlePlan(plan api.Plan) string {
	s := string(plan)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatPrice(millicents int64) string {
	if millicents == 0 {
		return "€0"
	}
	// The current catalog uses whole-euro prices. Keep the cents path for a
	// future plan so generation remains correct without a code change.
	whole := millicents / 100_000
	minor := (millicents % 100_000) / 1_000
	if minor == 0 {
		return fmt.Sprintf("€%d", whole)
	}
	return fmt.Sprintf("€%d.%02d", whole, minor)
}

func formatDuration(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}
