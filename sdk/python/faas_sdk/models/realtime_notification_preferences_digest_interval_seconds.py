from typing import Literal

RealtimeNotificationPreferencesDigestIntervalSeconds = Literal[0, 300, 3600]

REALTIME_NOTIFICATION_PREFERENCES_DIGEST_INTERVAL_SECONDS_VALUES: set[
    RealtimeNotificationPreferencesDigestIntervalSeconds
] = {
    0,
    300,
    3600,
}


def check_realtime_notification_preferences_digest_interval_seconds(
    value: int,
) -> RealtimeNotificationPreferencesDigestIntervalSeconds:
    if value in REALTIME_NOTIFICATION_PREFERENCES_DIGEST_INTERVAL_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_NOTIFICATION_PREFERENCES_DIGEST_INTERVAL_SECONDS_VALUES!r}"
    )
