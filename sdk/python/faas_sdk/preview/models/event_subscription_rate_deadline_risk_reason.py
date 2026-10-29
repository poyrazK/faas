from typing import Literal

EventSubscriptionRateDeadlineRiskReason = Literal["already_expired", "modeled_deadline_risk"]

EVENT_SUBSCRIPTION_RATE_DEADLINE_RISK_REASON_VALUES: set[EventSubscriptionRateDeadlineRiskReason] = {
    "already_expired",
    "modeled_deadline_risk",
}


def check_event_subscription_rate_deadline_risk_reason(value: str) -> EventSubscriptionRateDeadlineRiskReason:
    if value in EVENT_SUBSCRIPTION_RATE_DEADLINE_RISK_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_RATE_DEADLINE_RISK_REASON_VALUES!r}"
    )
