from typing import Literal

CreateReleaseGateRequestComparison = Literal["above", "below"]

CREATE_RELEASE_GATE_REQUEST_COMPARISON_VALUES: set[CreateReleaseGateRequestComparison] = {
    "above",
    "below",
}


def check_create_release_gate_request_comparison(value: str) -> CreateReleaseGateRequestComparison:
    if value in CREATE_RELEASE_GATE_REQUEST_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_RELEASE_GATE_REQUEST_COMPARISON_VALUES!r}")
