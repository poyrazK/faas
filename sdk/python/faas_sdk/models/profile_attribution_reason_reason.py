from typing import Literal

ProfileAttributionReasonReason = Literal[
    "attributed", "encoding_limit", "invalid_label", "route_not_admitted", "unknown", "unlabeled"
]

PROFILE_ATTRIBUTION_REASON_REASON_VALUES: set[ProfileAttributionReasonReason] = {
    "attributed",
    "encoding_limit",
    "invalid_label",
    "route_not_admitted",
    "unknown",
    "unlabeled",
}


def check_profile_attribution_reason_reason(value: str) -> ProfileAttributionReasonReason:
    if value in PROFILE_ATTRIBUTION_REASON_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_ATTRIBUTION_REASON_REASON_VALUES!r}")
