from typing import Literal

ProfileHeapResponseKind = Literal["heap"]

PROFILE_HEAP_RESPONSE_KIND_VALUES: set[ProfileHeapResponseKind] = {
    "heap",
}


def check_profile_heap_response_kind(value: str) -> ProfileHeapResponseKind:
    if value in PROFILE_HEAP_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_HEAP_RESPONSE_KIND_VALUES!r}")
