from typing import Literal

RuntimeReleaseResponseArchitecture = Literal["amd64", "arm64"]

RUNTIME_RELEASE_RESPONSE_ARCHITECTURE_VALUES: set[RuntimeReleaseResponseArchitecture] = {
    "amd64",
    "arm64",
}


def check_runtime_release_response_architecture(value: str) -> RuntimeReleaseResponseArchitecture:
    if value in RUNTIME_RELEASE_RESPONSE_ARCHITECTURE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_RELEASE_RESPONSE_ARCHITECTURE_VALUES!r}")
