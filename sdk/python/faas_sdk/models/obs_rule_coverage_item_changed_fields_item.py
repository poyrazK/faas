from typing import Literal

ObsRuleCoverageItemChangedFieldsItem = Literal["expression", "for", "keep_firing_for", "labels"]

OBS_RULE_COVERAGE_ITEM_CHANGED_FIELDS_ITEM_VALUES: set[ObsRuleCoverageItemChangedFieldsItem] = {
    "expression",
    "for",
    "keep_firing_for",
    "labels",
}


def check_obs_rule_coverage_item_changed_fields_item(value: str) -> ObsRuleCoverageItemChangedFieldsItem:
    if value in OBS_RULE_COVERAGE_ITEM_CHANGED_FIELDS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_ITEM_CHANGED_FIELDS_ITEM_VALUES!r}"
    )
