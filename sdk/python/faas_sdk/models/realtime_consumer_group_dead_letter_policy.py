from typing import Literal

RealtimeConsumerGroupDeadLetterPolicy = Literal["block", "skip"]

REALTIME_CONSUMER_GROUP_DEAD_LETTER_POLICY_VALUES: set[RealtimeConsumerGroupDeadLetterPolicy] = {
    "block",
    "skip",
}


def check_realtime_consumer_group_dead_letter_policy(value: str) -> RealtimeConsumerGroupDeadLetterPolicy:
    if value in REALTIME_CONSUMER_GROUP_DEAD_LETTER_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_CONSUMER_GROUP_DEAD_LETTER_POLICY_VALUES!r}"
    )
