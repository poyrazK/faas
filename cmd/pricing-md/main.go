// Command pricing-md generates the customer plan page from the authoritative
// api.LimitsFor table. Keeping the renderer in a small pure-Go command makes
// pricing reviewable and lets CI reject hand-edited plan numbers.
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
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
	b.WriteString("Gregale pricing and quotas come from [`pkg/api/limits.go`](../pkg/api/limits.go). The same table is enforced by the API, so this page is generated rather than maintained separately. Prices are monthly and shown in US dollars. Usage beyond the included GB-RAM-hours is billed at $0.01 per GB-RAM-hour on paid plans.\n\n")
	b.WriteString("## At a glance\n\n")
	b.WriteString("| Plan | Monthly | Deployed apps | PR preview environments | Developer apps | Concurrent instances | RAM / app | Included GB-RAM-hours | App layer | Idle timeout | Max request timeout |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, plan := range api.Plans {
		l, ok := api.LimitsFor(plan)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| **%s** | %s | %d | %d | %d | %d | %d MB | %d | %d MB | %s | %s |\n",
			titlePlan(plan), formatPrice(l.PriceMillicents), l.DeployedApps,
			l.PreviewApps, l.DeveloperApps, l.MaxConcurrency, l.RAMMB, l.IncludedGBHours,
			l.AppLayerMaxMB,
			formatDuration(l.IdleTimeoutS), formatDuration(int(l.RequestBudgetMaxDuration().Seconds())))
	}
	b.WriteString("\n## What each limit means\n\n")
	b.WriteString("- **Deployed apps** is the maximum number of production app records on the plan. `gregale dev` environments have a separate developer-app allowance.\n")
	b.WriteString("- **PR preview environments** is the separate limit for temporary pull-request previews: one per open pull request, however many workloads it runs. One environment runs at most as many workloads as the plan can deploy, and a closed pull request frees its environment at once. See [PR preview environments](preview-environments.md#quota).\n")
	b.WriteString("- **Concurrent instances** is the per-app wake/instance ceiling; request concurrency inside one VM is separately bounded by the plan.\n")
	b.WriteString("- **RAM / app** and **app layer** are hard build/runtime ceilings. Smaller resource profiles remain available where the plan permits them.\n")
	b.WriteString("- **Included GB-RAM-hours** is the monthly compute allowance. Free stops at its allowance; paid plans can accrue overage at the published rate.\n")
	b.WriteString("- **Idle timeout** is when an inactive app is parked. A later request wakes it from its snapshot; see [scale-to-zero](cold-wake.md).")
	fmt.Fprintf(&b, " Functions default to %s on every plan; any app or function can set its own value from %s up to twice the plan default.\n",
		formatDuration(api.FunctionIdleTimeoutDefaultSeconds), formatDuration(api.IdleTimeoutFloorSeconds))
	fmt.Fprintf(&b, "- **Max request timeout** is the longest `request_timeout_s` an app or function may set (`gregale app APP --request-timeout SECONDS`). Without one, a function request must start its response within %s and an app request within %s; a function's whole invocation, a streamed response included, may then run for the longer of its timeout and %s.\n\n",
		api.RequestBudgetDefault, api.RequestBudgetAppDefault, api.RequestBudgetMax)
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
	b.WriteString("\n## Managed realtime\n\n")
	b.WriteString("Realtime is included on every paid plan at no extra charge: Gregale bills compute, and channels, consumer groups and delivery are software. Limits apply only where realtime holds real capacity. Retained history is bounded per endpoint, and functions triggered by channel messages bill as normal compute.\n\n")
	b.WriteString("| Plan | Endpoints per app | Endpoints per account | Concurrent connections per account |\n")
	b.WriteString("|---|---:|---:|---:|\n")
	for _, plan := range api.Plans {
		rl, ok := api.RealtimeLimitsFor(plan)
		if !ok {
			continue
		}
		if rl.EndpointsPerApp == 0 {
			fmt.Fprintf(&b, "| **%s** | — | — | — |\n", titlePlan(plan))
			continue
		}
		fmt.Fprintf(&b, "| **%s** | %d | %d | %s |\n", titlePlan(plan), rl.EndpointsPerApp, rl.EndpointsPerAccount, formatCount(rl.ConnectionsPerAccount))
	}
	fmt.Fprintf(&b, "\nThe connection limit counts open WebSocket and SSE connections across all of an account's endpoints. One account may also hold at most %d%% of a single realtime node's connections; see [realtime fair use](realtime.md#fair-use-limits).\n",
		api.RealtimeAccountNodeSharePercent)
	renderObjectStorage(&b)
	b.WriteString("\n## Choose a plan\n\n")
	b.WriteString("Start on **Free** for a small public API or a trial. **Hobby** unlocks the paid observability, async, and data surfaces. **Pro** is the normal production tier for teams, while **Scale** raises the app, concurrency, RAM, and usage ceilings. Feature maturity and entitlement are listed in the [capability matrix](capabilities.md).\n\n")
	b.WriteString("Plan changes are safe to preview with `gregale plan`; quota errors include the exact observed value, limit, and next action.\n")
	return b.String()
}

// renderObjectStorage lists each plan's included object storage and the
// published rate beyond it (ADR-1430).
func renderObjectStorage(b *strings.Builder) {
	b.WriteString("\n## Object storage\n\n")
	fmt.Fprintf(b, "Object storage bills the bytes it holds. Each plan includes an average amount of stored data per month; storage beyond it costs %s per GiB-month. Requests and egress are not charged. Free stops at its included storage.\n\n",
		formatPrice(api.ObjectStorageMillicentsPerGiBMonth))
	b.WriteString("| Plan | Included storage | Beyond the included storage |\n")
	b.WriteString("|---|---:|---|\n")
	for _, plan := range api.Plans {
		l, ok := api.LimitsFor(plan)
		if !ok {
			continue
		}
		beyond := formatPrice(api.ObjectStorageMillicentsPerGiBMonth) + " per GiB-month"
		if l.ObjectStorage.MaxAccountBytes > 0 && l.ObjectStorage.MaxAccountBytes <= l.ObjectStorage.IncludedBytes {
			beyond = "stops at " + formatGiB(l.ObjectStorage.MaxAccountBytes)
		}
		fmt.Fprintf(b, "| **%s** | %s | %s |\n", titlePlan(plan), formatGiB(l.ObjectStorage.IncludedBytes), beyond)
	}
	b.WriteString("\nIncluded storage is the average over the month, so 20 GiB held for half a month uses 10 GiB of it. See [object storage](object-storage.md).\n")
}

func formatGiB(bytes int64) string {
	return strconv.FormatInt(bytes>>30, 10) + " GiB"
}

// formatCount groups thousands with commas, e.g. 5000 → "5,000".
func formatCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
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
		return api.BillingCurrencySymbol + "0"
	}
	// The current catalog uses whole-dollar prices. Keep the cents path for a
	// future plan so generation remains correct without a code change.
	whole := millicents / 100_000
	minor := (millicents % 100_000) / 1_000
	if minor == 0 {
		return fmt.Sprintf("%s%d", api.BillingCurrencySymbol, whole)
	}
	return fmt.Sprintf("%s%d.%02d", api.BillingCurrencySymbol, whole, minor)
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
