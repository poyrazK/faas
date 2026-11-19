from typing import Literal

InsightFunnelUnit = Literal["customer", "subject"]

INSIGHT_FUNNEL_UNIT_VALUES: set[InsightFunnelUnit] = {
    "customer",
    "subject",
}


def check_insight_funnel_unit(value: str) -> InsightFunnelUnit:
    if value in INSIGHT_FUNNEL_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_FUNNEL_UNIT_VALUES!r}")
