from typing import Literal

ScalingPolicyConcurrencyOverflow = Literal["drop", "queue"]

SCALING_POLICY_CONCURRENCY_OVERFLOW_VALUES: set[ScalingPolicyConcurrencyOverflow] = {
    "drop",
    "queue",
}


def check_scaling_policy_concurrency_overflow(value: str) -> ScalingPolicyConcurrencyOverflow:
    if value in SCALING_POLICY_CONCURRENCY_OVERFLOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCALING_POLICY_CONCURRENCY_OVERFLOW_VALUES!r}")
