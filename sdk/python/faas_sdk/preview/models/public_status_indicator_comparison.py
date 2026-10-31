from typing import Literal

PublicStatusIndicatorComparison = Literal["gte", "lte"]

PUBLIC_STATUS_INDICATOR_COMPARISON_VALUES: set[PublicStatusIndicatorComparison] = {
    "gte",
    "lte",
}


def check_public_status_indicator_comparison(value: str) -> PublicStatusIndicatorComparison:
    if value in PUBLIC_STATUS_INDICATOR_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_INDICATOR_COMPARISON_VALUES!r}")
