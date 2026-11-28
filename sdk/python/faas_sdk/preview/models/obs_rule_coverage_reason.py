from typing import Literal

ObsRuleCoverageReason = Literal[
    "comparison_incomplete",
    "deadline_exceeded",
    "expected_catalog_unavailable",
    "expected_rules_differ",
    "expected_set_not_selected",
    "expected_set_unavailable",
    "prometheus_not_configured",
    "source_unavailable",
]

OBS_RULE_COVERAGE_REASON_VALUES: set[ObsRuleCoverageReason] = {
    "comparison_incomplete",
    "deadline_exceeded",
    "expected_catalog_unavailable",
    "expected_rules_differ",
    "expected_set_not_selected",
    "expected_set_unavailable",
    "prometheus_not_configured",
    "source_unavailable",
}


def check_obs_rule_coverage_reason(value: str) -> ObsRuleCoverageReason:
    if value in OBS_RULE_COVERAGE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_REASON_VALUES!r}")
