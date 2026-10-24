from typing import Literal

CreateReleaseGateRequestKind = Literal["ai", "custom_metric", "new_errors", "slo"]

CREATE_RELEASE_GATE_REQUEST_KIND_VALUES: set[CreateReleaseGateRequestKind] = {
    "ai",
    "custom_metric",
    "new_errors",
    "slo",
}


def check_create_release_gate_request_kind(value: str) -> CreateReleaseGateRequestKind:
    if value in CREATE_RELEASE_GATE_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_RELEASE_GATE_REQUEST_KIND_VALUES!r}")
