from typing import Literal

SavedInsightResultKind = Literal["events", "funnel", "retention"]

SAVED_INSIGHT_RESULT_KIND_VALUES: set[SavedInsightResultKind] = {
    "events",
    "funnel",
    "retention",
}


def check_saved_insight_result_kind(value: str) -> SavedInsightResultKind:
    if value in SAVED_INSIGHT_RESULT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAVED_INSIGHT_RESULT_KIND_VALUES!r}")
