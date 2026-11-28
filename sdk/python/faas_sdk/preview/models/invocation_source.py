from typing import Literal

InvocationSource = Literal["async_invoke", "cron", "delayed_task", "inbound_webhook", "queue", "replay"]

INVOCATION_SOURCE_VALUES: set[InvocationSource] = {
    "async_invoke",
    "cron",
    "delayed_task",
    "inbound_webhook",
    "queue",
    "replay",
}


def check_invocation_source(value: str) -> InvocationSource:
    if value in INVOCATION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOCATION_SOURCE_VALUES!r}")
