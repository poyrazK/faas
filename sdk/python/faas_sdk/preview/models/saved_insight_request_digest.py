from typing import Literal

SavedInsightRequestDigest = Literal["daily", "weekly"]

SAVED_INSIGHT_REQUEST_DIGEST_VALUES: set[SavedInsightRequestDigest] = {
    "daily",
    "weekly",
}


def check_saved_insight_request_digest(value: str) -> SavedInsightRequestDigest:
    if value in SAVED_INSIGHT_REQUEST_DIGEST_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAVED_INSIGHT_REQUEST_DIGEST_VALUES!r}")
