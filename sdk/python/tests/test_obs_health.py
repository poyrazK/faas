import unittest

from faas_sdk.models import ObsHealthResponse


class ObsHealthContractTest(unittest.TestCase):
    def snapshot(self):
        return {
            "generated_at": "2026-10-15T12:00:00+00:00",
            "prometheus_available": False,
            "audit_log_write_total_5m": 0,
            "audit_log_write_failures_5m": 0,
            "audit_log_coverage_ratio_5m": 1.0,
            "operator_intent_outcome_missing_total": {},
            "trace_id_completeness_ratio": {},
            "alerts_firing": 0,
        }

    def test_independent_signal_evidence_roundtrips(self):
        payload = self.snapshot()
        payload["signals"] = {
            "audit_log_write_total_5m": {
                "state": "healthy",
                "observed_at": "2026-10-15T11:59:45+00:00",
                "window_seconds": 300,
                "max_age_seconds": 120,
            },
            "audit_log_write_failures_5m": {
                "state": "stale",
                "observed_at": "2026-10-15T11:50:00+00:00",
                "window_seconds": 300,
                "max_age_seconds": 120,
                "reason": "observation_stale",
            },
            "alerts_firing": {
                "state": "unavailable",
                "window_seconds": 0,
                "max_age_seconds": 120,
                "reason": "query_failed",
            },
        }
        response = ObsHealthResponse.from_dict(payload)
        self.assertEqual(response.signals["audit_log_write_total_5m"].state, "healthy")
        self.assertEqual(response.to_dict(), payload)
        payload["signals"]["alerts_firing"].update(state="no_samples", reason="missing_observation")
        self.assertEqual(ObsHealthResponse.from_dict(payload).to_dict(), payload)

    def test_legacy_response_does_not_manufacture_signal_evidence(self):
        payload = self.snapshot()
        self.assertEqual(ObsHealthResponse.from_dict(payload).to_dict(), payload)
