from typing import Literal

TriggerKind = Literal["cron", "kafka", "nats", "queue", "realtime", "redis_streams", "sqs_compat"]

TRIGGER_KIND_VALUES: set[TriggerKind] = {
    "cron",
    "kafka",
    "nats",
    "queue",
    "realtime",
    "redis_streams",
    "sqs_compat",
}


def check_trigger_kind(value: str) -> TriggerKind:
    if value in TRIGGER_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_KIND_VALUES!r}")
