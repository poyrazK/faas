package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dataapi"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type DataAPIData struct {
	Slug, CSRFToken, Message, TaskID, TaskStatus, Fingerprint string
	Enabled, TypesReady                                       bool
	RefreshSeconds                                            int
	Bindings                                                  []managedpostgres.AppBindingInventory
	Schema                                                    *dataapi.Snapshot
	// Usage is nil when usage storage is unavailable (ADR-20261019102108).
	Usage        *DataAPIUsageView
	UsageMessage string
	// Access lists which owner settings are present. Their values are sealed
	// app secrets, so the dashboard shows presence and the CLI change.
	Access     []DataAPISetting
	ServerKeys []DataAPIServerKeySlot
}

// DataAPIUsageDay is one row of the usage table.
type DataAPIUsageDay struct {
	Label                                                              string
	Requests, Anonymous, Server, Rejected, Errors, Capped, Rows, Bytes int64
}

// DataAPIUsageView is the usage section: recent days and this month.
type DataAPIUsageView struct {
	Month                         string
	MonthRequests, MonthAnonymous int64
	Days                          []DataAPIUsageDay
	Totals                        DataAPIUsageDay
	// Max scales the per-day meters; at least 1 so each meter is valid.
	Max int64
}

// NewDataAPIUsageView shapes an owner usage view for the page, newest day first.
func NewDataAPIUsageView(u api.DataAPIUsage) *DataAPIUsageView {
	day := func(label string, c api.DataAPIUsageCounts) DataAPIUsageDay {
		return DataAPIUsageDay{Label: label, Requests: c.UserRequests + c.AnonymousRequests + c.ServerRequests, Anonymous: c.AnonymousRequests, Server: c.ServerRequests,
			Rejected: c.RejectedRequests, Errors: c.ErrorRequests, Capped: c.CappedRequests, Rows: c.RowsReturned, Bytes: c.ResponseBytes}
	}
	view := &DataAPIUsageView{Month: u.MonthToDate.Month, MonthRequests: u.MonthToDate.Requests, MonthAnonymous: u.MonthToDate.AnonymousRequests,
		Totals: day("Total", u.Totals), Max: 1}
	for i := len(u.Buckets) - 1; i >= 0; i-- {
		d := day(u.Buckets[i].Start.UTC().Format("2006-01-02"), u.Buckets[i].DataAPIUsageCounts)
		view.Max = max(view.Max, d.Requests)
		view.Days = append(view.Days, d)
	}
	return view
}

// DataAPISetting is one owner setting and the CLI command that changes it.
type DataAPISetting struct {
	Name, State, Command string
}

// DataAPIServerKeySlot is one of the two server-key rotation slots.
type DataAPIServerKeySlot struct {
	Slot              string
	InUse, Restricted bool
}

// DataAPIAccessSettings reports the Data API settings among an app's secret
// keys. Permissions left by a removed key do not count as restricted.
func DataAPIAccessSettings(slug string, keys map[string]bool) ([]DataAPISetting, []DataAPIServerKeySlot) {
	state := func(key, on, off string) string {
		if keys[key] {
			return on
		}
		return off
	}
	configure := "gregale data-api configure " + slug + " "
	settings := []DataAPISetting{
		{"Public reads", state("DATA_API_PUBLIC_READ", "On", "Off"), configure + "--public-read SCHEMA.RELATION[,...] | --no-public-read"},
		{"Public RPCs", state("DATA_API_PUBLIC_RPC", "On", "Off"), configure + "--public-rpc SCHEMA.FUNCTION[,...] | --no-public-rpc"},
		{"Edge cache for public reads", state("DATA_API_PUBLIC_READ_MAX_AGE", "On", "Off"), configure + "--public-read-cache DURATION|off"},
		{"Aggregates", state("DATA_API_AGGREGATES", "On", "Off"), configure + "--aggregates on|off"},
		{"Usage reporting", state("DATA_API_USAGE_URL", "On", "Off"), configure + "--usage-reporting on|off"},
		{"Monthly caps", state("DATA_API_MONTHLY_CAPS", "Set", "Not set"), configure + "--monthly-caps requests=N[,anonymous=N]|off"},
	}
	slots := []DataAPIServerKeySlot{{Slot: "a"}, {Slot: "b"}}
	for i := range slots {
		prefix := "DATA_API_SERVER_KEY_" + map[string]string{"a": "A", "b": "B"}[slots[i].Slot]
		slots[i].InUse = keys[prefix+"_SHA256"]
		slots[i].Restricted = slots[i].InUse && keys[prefix+"_PERMISSIONS"]
	}
	return settings, slots
}
