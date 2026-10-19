from typing import Literal

ObsRuleHealthItemIssuesItem = Literal[
    "duplicate_group_identity",
    "duplicate_rule_identity",
    "evaluation_clock_in_future",
    "evaluation_failed",
    "evaluation_stale",
    "evaluation_unavailable",
    "inconsistent_rule_state",
    "invalid_alert_observation",
    "invalid_group_identity",
    "invalid_rule_definition",
    "missing_evaluation",
    "unexpected_rule_type",
]

OBS_RULE_HEALTH_ITEM_ISSUES_ITEM_VALUES: set[ObsRuleHealthItemIssuesItem] = {
    "duplicate_group_identity",
    "duplicate_rule_identity",
    "evaluation_clock_in_future",
    "evaluation_failed",
    "evaluation_stale",
    "evaluation_unavailable",
    "inconsistent_rule_state",
    "invalid_alert_observation",
    "invalid_group_identity",
    "invalid_rule_definition",
    "missing_evaluation",
    "unexpected_rule_type",
}


def check_obs_rule_health_item_issues_item(value: str) -> ObsRuleHealthItemIssuesItem:
    if value in OBS_RULE_HEALTH_ITEM_ISSUES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_HEALTH_ITEM_ISSUES_ITEM_VALUES!r}")
