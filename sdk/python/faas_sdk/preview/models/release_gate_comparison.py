from typing import Literal

ReleaseGateComparison = Literal["above", "below"]

RELEASE_GATE_COMPARISON_VALUES: set[ReleaseGateComparison] = {
    "above",
    "below",
}


def check_release_gate_comparison(value: str) -> ReleaseGateComparison:
    if value in RELEASE_GATE_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_GATE_COMPARISON_VALUES!r}")
