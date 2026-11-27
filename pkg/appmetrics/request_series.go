package appmetrics

import "fmt"

type requestSeries struct {
	duration, capacityRefusals, labels string
}

func requestSeriesForScope(appID, scope string) requestSeries {
	if scope == "" {
		return requestSeries{
			duration: "gateway_request_duration_seconds", capacityRefusals: "gateway_capacity_refusals_total",
			labels: fmt.Sprintf(`app=%q`, appID),
		}
	}
	return requestSeries{
		duration: "gateway_environment_request_duration_seconds", capacityRefusals: "gateway_environment_capacity_refusals_total",
		labels: fmt.Sprintf(`app=%q,scope=%q`, appID, scope),
	}
}

const successfulRequestClass = `,class="2xx"`
const failedRequestClass = `,class="5xx"`
const eligibleRequestClasses = `,class=~"2xx|5xx"`

func (s requestSeries) counter(class string) string {
	return fmt.Sprintf(`%s_count{%s%s}`, s.duration, s.labels, class)
}

func (s requestSeries) requestsWithRefusals(fn, window, class string) string {
	return fmt.Sprintf(`sum(%[1]s(%[2]s[%[3]s]) or %[1]s(%[4]s{%[5]s}[%[3]s]))`,
		fn, s.counter(class), window, s.capacityRefusals, s.labels)
}

func (s requestSeries) errorRate(window string) string {
	return fmt.Sprintf(`(%s) or vector(0)`, PercentRatioQuery(
		s.requestsWithRefusals("rate", window, failedRequestClass),
		s.requestsWithRefusals("rate", window, eligibleRequestClasses)))
}

func (s requestSeries) latency(window string, quantile float64) string {
	return HistogramQuantileMSQuery(quantile,
		fmt.Sprintf(`sum by (le) (rate(%s_bucket{%s%s}[%s]))`, s.duration, s.labels, successfulRequestClass, window),
		fmt.Sprintf(`sum(rate(%s[%s]))`, s.counter(successfulRequestClass), window))
}
