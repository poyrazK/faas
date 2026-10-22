from typing import Literal

ReleaseGateResultKind = Literal["custom_metric", "metric_change", "new_errors", "slo", "synthetic"]

RELEASE_GATE_RESULT_KIND_VALUES: set[ReleaseGateResultKind] = {
    "custom_metric",
    "metric_change",
    "new_errors",
    "slo",
    "synthetic",
}


def check_release_gate_result_kind(value: str) -> ReleaseGateResultKind:
    if value in RELEASE_GATE_RESULT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_GATE_RESULT_KIND_VALUES!r}")
