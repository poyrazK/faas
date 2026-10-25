from typing import Literal

EventSubscriptionRatePreviewSampleLimit = Literal[1000]

EVENT_SUBSCRIPTION_RATE_PREVIEW_SAMPLE_LIMIT_VALUES: set[EventSubscriptionRatePreviewSampleLimit] = {
    1000,
}


def check_event_subscription_rate_preview_sample_limit(value: int) -> EventSubscriptionRatePreviewSampleLimit:
    if value in EVENT_SUBSCRIPTION_RATE_PREVIEW_SAMPLE_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_RATE_PREVIEW_SAMPLE_LIMIT_VALUES!r}"
    )
