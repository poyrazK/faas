package api

import (
	"encoding/csv"
	"io"
	"strconv"
	"time"
)

// WriteSavedInsightCSV writes a saved insight's result as CSV: one row per point of an
// events series, per step of a funnel (and of each breakdown), and per
// period of each retention row.
func WriteSavedInsightCSV(w io.Writer, res SavedInsightResult) error {
	cw := csv.NewWriter(w)
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	d := func(v int64) string { return strconv.FormatInt(v, 10) }
	switch {
	case res.Events != nil:
		_ = cw.Write([]string{"start", "breakdown", "events", "units", "value"})
		for _, p := range res.Events.Points {
			_ = cw.Write([]string{p.Start.UTC().Format(time.RFC3339), p.Breakdown, d(p.Events), d(p.Units), d(p.Value)})
		}
	case res.Funnel != nil:
		_ = cw.Write([]string{"breakdown", "step", "event", "units", "conversion_from_first", "conversion_from_previous"})
		rows := append([]InsightFunnelBreakdown{{Steps: res.Funnel.Steps}}, res.Funnel.Breakdowns...)
		for _, b := range rows {
			for i, s := range b.Steps {
				_ = cw.Write([]string{b.Value, strconv.Itoa(i + 1), s.Event, d(s.Units), f(s.ConversionFromFirst), f(s.ConversionFromPrev)})
			}
		}
	case res.Retention != nil:
		_ = cw.Write([]string{"cohort", "breakdown", "period", "eligible", "returned", "rate"})
		write := func(cohort string, r InsightRetentionRow) {
			for i := range r.Rates {
				var eligible, returned int64
				if i < len(r.Eligible) {
					eligible = r.Eligible[i]
				}
				if i < len(r.Returned) {
					returned = r.Returned[i]
				}
				_ = cw.Write([]string{cohort, r.Value, strconv.Itoa(i + 1), d(eligible), d(returned), f(r.Rates[i])})
			}
		}
		write("overall", res.Retention.Overall)
		for _, r := range res.Retention.Cohorts {
			cohort := ""
			if r.Start != nil {
				cohort = r.Start.UTC().Format(time.RFC3339)
			}
			write(cohort, r)
		}
		for _, r := range res.Retention.Breakdowns {
			write("overall", r)
		}
	}
	cw.Flush()
	return cw.Error()
}
