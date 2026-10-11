from typing import Literal

RuntimeReleaseResponseQualification = Literal["not_evaluated"]

RUNTIME_RELEASE_RESPONSE_QUALIFICATION_VALUES: set[RuntimeReleaseResponseQualification] = {
    "not_evaluated",
}


def check_runtime_release_response_qualification(value: str) -> RuntimeReleaseResponseQualification:
    if value in RUNTIME_RELEASE_RESPONSE_QUALIFICATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_RELEASE_RESPONSE_QUALIFICATION_VALUES!r}")
