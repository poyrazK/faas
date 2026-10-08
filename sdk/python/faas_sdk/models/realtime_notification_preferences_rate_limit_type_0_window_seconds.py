from typing import Literal

RealtimeNotificationPreferencesRateLimitType0WindowSeconds = Literal[60, 300, 3600]

REALTIME_NOTIFICATION_PREFERENCES_RATE_LIMIT_TYPE_0_WINDOW_SECONDS_VALUES: set[
    RealtimeNotificationPreferencesRateLimitType0WindowSeconds
] = {
    60,
    300,
    3600,
}


def check_realtime_notification_preferences_rate_limit_type_0_window_seconds(
    value: int,
) -> RealtimeNotificationPreferencesRateLimitType0WindowSeconds:
    if value in REALTIME_NOTIFICATION_PREFERENCES_RATE_LIMIT_TYPE_0_WINDOW_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_NOTIFICATION_PREFERENCES_RATE_LIMIT_TYPE_0_WINDOW_SECONDS_VALUES!r}"
    )
