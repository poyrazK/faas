from typing import Literal

ScenarioCapturedQueueMessageKind = Literal["delayed_task", "queue"]

SCENARIO_CAPTURED_QUEUE_MESSAGE_KIND_VALUES: set[ScenarioCapturedQueueMessageKind] = {
    "delayed_task",
    "queue",
}


def check_scenario_captured_queue_message_kind(value: str) -> ScenarioCapturedQueueMessageKind:
    if value in SCENARIO_CAPTURED_QUEUE_MESSAGE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_CAPTURED_QUEUE_MESSAGE_KIND_VALUES!r}")
