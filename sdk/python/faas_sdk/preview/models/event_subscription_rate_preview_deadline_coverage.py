from typing import Literal

EventSubscriptionRatePreviewDeadlineCoverage = Literal["sampled_effective_pending_deadlines"]

EVENT_SUBSCRIPTION_RATE_PREVIEW_DEADLINE_COVERAGE_VALUES: set[EventSubscriptionRatePreviewDeadlineCoverage] = {
    "sampled_effective_pending_deadlines",
}


def check_event_subscription_rate_preview_deadline_coverage(value: str) -> EventSubscriptionRatePreviewDeadlineCoverage:
    if value in EVENT_SUBSCRIPTION_RATE_PREVIEW_DEADLINE_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_RATE_PREVIEW_DEADLINE_COVERAGE_VALUES!r}"
    )
