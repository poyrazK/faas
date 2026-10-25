from typing import Literal

OperationWebhookBindingResponseMode = Literal["signal", "start"]

OPERATION_WEBHOOK_BINDING_RESPONSE_MODE_VALUES: set[OperationWebhookBindingResponseMode] = {
    "signal",
    "start",
}


def check_operation_webhook_binding_response_mode(value: str) -> OperationWebhookBindingResponseMode:
    if value in OPERATION_WEBHOOK_BINDING_RESPONSE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WEBHOOK_BINDING_RESPONSE_MODE_VALUES!r}")
