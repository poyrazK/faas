from typing import Literal

ReleaseGateKind = Literal["custom_metric", "new_errors", "slo"]

RELEASE_GATE_KIND_VALUES: set[ReleaseGateKind] = {
    "custom_metric",
    "new_errors",
    "slo",
}


def check_release_gate_kind(value: str) -> ReleaseGateKind:
    if value in RELEASE_GATE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_GATE_KIND_VALUES!r}")
