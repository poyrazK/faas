from typing import Literal

InsightRetentionRequestUnit = Literal["customer", "subject"]

INSIGHT_RETENTION_REQUEST_UNIT_VALUES: set[InsightRetentionRequestUnit] = {
    "customer",
    "subject",
}


def check_insight_retention_request_unit(value: str) -> InsightRetentionRequestUnit:
    if value in INSIGHT_RETENTION_REQUEST_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_RETENTION_REQUEST_UNIT_VALUES!r}")
