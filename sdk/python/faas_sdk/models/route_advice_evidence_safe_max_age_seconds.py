from typing import Literal

RouteAdviceEvidenceSafeMaxAgeSeconds = Literal[60, 300, 3600]

ROUTE_ADVICE_EVIDENCE_SAFE_MAX_AGE_SECONDS_VALUES: set[RouteAdviceEvidenceSafeMaxAgeSeconds] = {
    60,
    300,
    3600,
}


def check_route_advice_evidence_safe_max_age_seconds(value: int) -> RouteAdviceEvidenceSafeMaxAgeSeconds:
    if value in ROUTE_ADVICE_EVIDENCE_SAFE_MAX_AGE_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_EVIDENCE_SAFE_MAX_AGE_SECONDS_VALUES!r}"
    )
