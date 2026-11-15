"""Request analytics response contracts shared by stable and preview clients."""

import importlib
import unittest


class AnalyticsContracts(unittest.TestCase):
    def test_grouped_points_remain_an_array(self):
        stamp = "2026-11-11T12:00:00Z"
        point = dict(start=stamp, requests=1, error_requests=0, error_rate_pct=0,
                     cold_boots=0, p50_ms=10, p95_ms=10, p99_ms=10)
        payload = dict(slug="analytics", since="1h", **{"from": stamp}, until=stamp,
                       window_clamped=False, bucket="1h", points=[], as_of=stamp,
                       group_by="country", series=[dict(value="DE", points=[point])])
        for prefix in ("faas_sdk", "faas_sdk.preview"):
            with self.subTest(client=prefix):
                model = importlib.import_module(
                    prefix + ".models.request_analytics_timeseries_response"
                ).RequestAnalyticsTimeseriesResponse.from_dict(payload)
                self.assertEqual(model.points, [])
                self.assertEqual(model.series[0].points[0].requests, 1)

    def test_bounded_other_method_and_unknown_coverage(self):
        payload = dict(value="__route_other__", method="OTHER", requests=1,
                       error_requests=0, error_rate_pct=0, cold_boots=0,
                       p50_ms=10, p95_ms=10, p99_ms=10)
        for prefix in ("faas_sdk", "faas_sdk.preview"):
            with self.subTest(client=prefix):
                group = importlib.import_module(prefix + ".models.request_analytics_group")
                self.assertEqual(group.RequestAnalyticsGroup.from_dict(payload).method, "OTHER")
                gap = importlib.import_module(prefix + ".models.request_analytics_telemetry_gap")
                self.assertEqual(gap.check_request_analytics_telemetry_gap(
                    "telemetry_coverage_unknown"), "telemetry_coverage_unknown")


if __name__ == "__main__":
    unittest.main()
