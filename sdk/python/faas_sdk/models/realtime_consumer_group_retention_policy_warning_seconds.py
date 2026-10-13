from typing import Literal

RealtimeConsumerGroupRetentionPolicyWarningSeconds = Literal[300]

REALTIME_CONSUMER_GROUP_RETENTION_POLICY_WARNING_SECONDS_VALUES: set[
    RealtimeConsumerGroupRetentionPolicyWarningSeconds
] = {
    300,
}


def check_realtime_consumer_group_retention_policy_warning_seconds(
    value: int,
) -> RealtimeConsumerGroupRetentionPolicyWarningSeconds:
    if value in REALTIME_CONSUMER_GROUP_RETENTION_POLICY_WARNING_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_RETENTION_POLICY_WARNING_SECONDS_VALUES!r}"
    )
