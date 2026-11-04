from typing import Literal

ObsRuleHealthItemKind = Literal["group", "rule"]

OBS_RULE_HEALTH_ITEM_KIND_VALUES: set[ObsRuleHealthItemKind] = {
    "group",
    "rule",
}


def check_obs_rule_health_item_kind(value: str) -> ObsRuleHealthItemKind:
    if value in OBS_RULE_HEALTH_ITEM_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_HEALTH_ITEM_KIND_VALUES!r}")
