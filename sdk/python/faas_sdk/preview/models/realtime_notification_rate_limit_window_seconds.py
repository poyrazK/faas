from typing import Literal

RealtimeNotificationRateLimitWindowSeconds = Literal[60, 300, 3600]

REALTIME_NOTIFICATION_RATE_LIMIT_WINDOW_SECONDS_VALUES: set[RealtimeNotificationRateLimitWindowSeconds] = {
    60,
    300,
    3600,
}


def check_realtime_notification_rate_limit_window_seconds(value: int) -> RealtimeNotificationRateLimitWindowSeconds:
    if value in REALTIME_NOTIFICATION_RATE_LIMIT_WINDOW_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_NOTIFICATION_RATE_LIMIT_WINDOW_SECONDS_VALUES!r}"
    )
