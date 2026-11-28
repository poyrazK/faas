from typing import Literal

RuntimeReleaseResponseRuntime = Literal["go124", "go124-alpine", "node22", "node24", "python312", "python313"]

RUNTIME_RELEASE_RESPONSE_RUNTIME_VALUES: set[RuntimeReleaseResponseRuntime] = {
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_runtime_release_response_runtime(value: str) -> RuntimeReleaseResponseRuntime:
    if value in RUNTIME_RELEASE_RESPONSE_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_RELEASE_RESPONSE_RUNTIME_VALUES!r}")
