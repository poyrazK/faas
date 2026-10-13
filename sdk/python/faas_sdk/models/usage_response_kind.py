from typing import Literal

UsageResponseKind = Literal["app", "job", "run"]

USAGE_RESPONSE_KIND_VALUES: set[UsageResponseKind] = {
    "app",
    "job",
    "run",
}


def check_usage_response_kind(value: str) -> UsageResponseKind:
    if value in USAGE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {USAGE_RESPONSE_KIND_VALUES!r}")
