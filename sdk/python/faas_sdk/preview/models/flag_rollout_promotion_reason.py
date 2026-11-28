from typing import Literal

FlagRolloutPromotionReason = Literal[
    "all_stages_complete", "http_5xx_rate_exceeded", "insufficient_used_requests", "p95_latency_exceeded"
]

FLAG_ROLLOUT_PROMOTION_REASON_VALUES: set[FlagRolloutPromotionReason] = {
    "all_stages_complete",
    "http_5xx_rate_exceeded",
    "insufficient_used_requests",
    "p95_latency_exceeded",
}


def check_flag_rollout_promotion_reason(value: str) -> FlagRolloutPromotionReason:
    if value in FLAG_ROLLOUT_PROMOTION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_ROLLOUT_PROMOTION_REASON_VALUES!r}")
