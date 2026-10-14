from typing import Literal

ProfileHeapResponseUnit = Literal["bytes"]

PROFILE_HEAP_RESPONSE_UNIT_VALUES: set[ProfileHeapResponseUnit] = {
    "bytes",
}


def check_profile_heap_response_unit(value: str) -> ProfileHeapResponseUnit:
    if value in PROFILE_HEAP_RESPONSE_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_HEAP_RESPONSE_UNIT_VALUES!r}")
