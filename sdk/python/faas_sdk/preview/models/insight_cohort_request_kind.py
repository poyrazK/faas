from typing import Literal

InsightCohortRequestKind = Literal["did", "did_not", "funnel"]

INSIGHT_COHORT_REQUEST_KIND_VALUES: set[InsightCohortRequestKind] = {
    "did",
    "did_not",
    "funnel",
}


def check_insight_cohort_request_kind(value: str) -> InsightCohortRequestKind:
    if value in INSIGHT_COHORT_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_COHORT_REQUEST_KIND_VALUES!r}")
