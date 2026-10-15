package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdRealtimeUsage shows the account's realtime connections against its
// plan's fair-use limit and recorded usage per hour (ADR-1323).
func cmdRealtimeUsage(args []string) int {
	fs := newFlagSet("realtime usage", flag.ContinueOnError)
	hours := fs.Int("hours", api.RealtimeUsageQueryDefaultHours, "UTC hours of usage to show (1-744)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *hours < 1 || *hours > api.RealtimeUsageQueryMaxHours {
		PrintUsage(os.Stderr, "usage: gregale realtime usage [--hours N]", "realtime")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	usage, err := client.GetRealtimeUsage(context.Background(), *hours)
	if err != nil {
		return printErr("Could not read realtime usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(usage))
	}
	printRealtimeUsage(usage, *hours)
	return 0
}

func printRealtimeUsage(usage api.RealtimeUsageResponse, hours int) {
	if usage.ConnectionLimit == 0 {
		_, _ = fmt.Fprintf(osStdout, "The %s plan does not include realtime.\n", usage.Plan)
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Realtime connections: %s open of %s allowed on the %s plan. Realtime is free; the limit is fair use.\n",
		groupDigits(usage.OpenConnections), groupDigits(int64(usage.ConnectionLimit)), usage.Plan)
	if len(usage.Hours) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No recorded realtime usage in the last %d hours.\n", hours)
		return
	}
	_, _ = fmt.Fprintln(osStdout)
	_, _ = fmt.Fprintf(osStdout, "%-17s %8s %12s %12s %12s\n", "HOUR (UTC)", "PEAK", "CONN-HOURS", "DELIVERED", "RECEIVED")
	for _, h := range usage.Hours {
		_, _ = fmt.Fprintf(osStdout, "%-17s %8s %12s %12s %12s\n", h.Hour.UTC().Format("2006-01-02 15:04"), groupDigits(h.PeakConnections),
			strconv.FormatFloat(float64(h.ConnectionSeconds)/3600, 'f', 1, 64), groupDigits(h.DeliveredMessages), groupDigits(h.ReceivedMessages))
	}
}

// groupDigits writes n with thousands separators, e.g. 12401 → "12,401".
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}
