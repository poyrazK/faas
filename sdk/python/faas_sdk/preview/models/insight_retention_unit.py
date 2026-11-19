from typing import Literal

InsightRetentionUnit = Literal["customer", "subject"]

INSIGHT_RETENTION_UNIT_VALUES: set[InsightRetentionUnit] = {
    "customer",
    "subject",
}


def check_insight_retention_unit(value: str) -> InsightRetentionUnit:
    if value in INSIGHT_RETENTION_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_RETENTION_UNIT_VALUES!r}")
