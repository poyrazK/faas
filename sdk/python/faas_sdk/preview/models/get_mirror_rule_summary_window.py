from typing import Literal

GetMirrorRuleSummaryWindow = Literal["1h", "24h", "7d"]

GET_MIRROR_RULE_SUMMARY_WINDOW_VALUES: set[GetMirrorRuleSummaryWindow] = {
    "1h",
    "24h",
    "7d",
}


def check_get_mirror_rule_summary_window(value: str) -> GetMirrorRuleSummaryWindow:
    if value in GET_MIRROR_RULE_SUMMARY_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_MIRROR_RULE_SUMMARY_WINDOW_VALUES!r}")
