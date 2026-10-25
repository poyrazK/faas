from typing import Literal

ReleaseGateResultKind = Literal["ai", "custom_metric", "new_errors", "slo"]

RELEASE_GATE_RESULT_KIND_VALUES: set[ReleaseGateResultKind] = {
    "ai",
    "custom_metric",
    "new_errors",
    "slo",
}


def check_release_gate_result_kind(value: str) -> ReleaseGateResultKind:
    if value in RELEASE_GATE_RESULT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_GATE_RESULT_KIND_VALUES!r}")
