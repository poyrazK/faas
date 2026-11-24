from typing import Literal

SavedInsightRequestKind = Literal["events", "funnel", "retention"]

SAVED_INSIGHT_REQUEST_KIND_VALUES: set[SavedInsightRequestKind] = {
    "events",
    "funnel",
    "retention",
}


def check_saved_insight_request_kind(value: str) -> SavedInsightRequestKind:
    if value in SAVED_INSIGHT_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAVED_INSIGHT_REQUEST_KIND_VALUES!r}")
