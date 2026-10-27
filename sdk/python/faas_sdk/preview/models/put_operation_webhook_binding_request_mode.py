from typing import Literal

PutOperationWebhookBindingRequestMode = Literal["signal", "start"]

PUT_OPERATION_WEBHOOK_BINDING_REQUEST_MODE_VALUES: set[PutOperationWebhookBindingRequestMode] = {
    "signal",
    "start",
}


def check_put_operation_webhook_binding_request_mode(value: str) -> PutOperationWebhookBindingRequestMode:
    if value in PUT_OPERATION_WEBHOOK_BINDING_REQUEST_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PUT_OPERATION_WEBHOOK_BINDING_REQUEST_MODE_VALUES!r}"
    )
