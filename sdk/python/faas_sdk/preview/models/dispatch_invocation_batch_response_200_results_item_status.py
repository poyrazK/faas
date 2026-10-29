from typing import Literal

DispatchInvocationBatchResponse200ResultsItemStatus = Literal["dead_letter", "retry", "succeeded"]

DISPATCH_INVOCATION_BATCH_RESPONSE_200_RESULTS_ITEM_STATUS_VALUES: set[
    DispatchInvocationBatchResponse200ResultsItemStatus
] = {
    "dead_letter",
    "retry",
    "succeeded",
}


def check_dispatch_invocation_batch_response_200_results_item_status(
    value: str,
) -> DispatchInvocationBatchResponse200ResultsItemStatus:
    if value in DISPATCH_INVOCATION_BATCH_RESPONSE_200_RESULTS_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DISPATCH_INVOCATION_BATCH_RESPONSE_200_RESULTS_ITEM_STATUS_VALUES!r}"
    )
