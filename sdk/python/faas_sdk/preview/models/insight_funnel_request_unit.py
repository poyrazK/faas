from typing import Literal

InsightFunnelRequestUnit = Literal["customer", "subject"]

INSIGHT_FUNNEL_REQUEST_UNIT_VALUES: set[InsightFunnelRequestUnit] = {
    "customer",
    "subject",
}


def check_insight_funnel_request_unit(value: str) -> InsightFunnelRequestUnit:
    if value in INSIGHT_FUNNEL_REQUEST_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_FUNNEL_REQUEST_UNIT_VALUES!r}")
