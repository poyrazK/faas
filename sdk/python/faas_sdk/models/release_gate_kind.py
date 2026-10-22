from typing import Literal

ReleaseGateKind = Literal["custom_metric", "metric_change", "new_errors", "slo", "synthetic"]

RELEASE_GATE_KIND_VALUES: set[ReleaseGateKind] = {
    "custom_metric",
    "metric_change",
    "new_errors",
    "slo",
    "synthetic",
}


def check_release_gate_kind(value: str) -> ReleaseGateKind:
    if value in RELEASE_GATE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_GATE_KIND_VALUES!r}")
