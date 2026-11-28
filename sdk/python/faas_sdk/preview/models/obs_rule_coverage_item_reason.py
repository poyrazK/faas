from typing import Literal

ObsRuleCoverageItemReason = Literal[
    "ambiguous_identity", "definition_changed", "definition_unavailable", "rule_missing", "source_identity_unavailable"
]

OBS_RULE_COVERAGE_ITEM_REASON_VALUES: set[ObsRuleCoverageItemReason] = {
    "ambiguous_identity",
    "definition_changed",
    "definition_unavailable",
    "rule_missing",
    "source_identity_unavailable",
}


def check_obs_rule_coverage_item_reason(value: str) -> ObsRuleCoverageItemReason:
    if value in OBS_RULE_COVERAGE_ITEM_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_ITEM_REASON_VALUES!r}")
