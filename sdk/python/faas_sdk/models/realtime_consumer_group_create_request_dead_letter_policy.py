from typing import Literal

RealtimeConsumerGroupCreateRequestDeadLetterPolicy = Literal["block", "skip"]

REALTIME_CONSUMER_GROUP_CREATE_REQUEST_DEAD_LETTER_POLICY_VALUES: set[
    RealtimeConsumerGroupCreateRequestDeadLetterPolicy
] = {
    "block",
    "skip",
}


def check_realtime_consumer_group_create_request_dead_letter_policy(
    value: str,
) -> RealtimeConsumerGroupCreateRequestDeadLetterPolicy:
    if value in REALTIME_CONSUMER_GROUP_CREATE_REQUEST_DEAD_LETTER_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_CREATE_REQUEST_DEAD_LETTER_POLICY_VALUES!r}"
    )
