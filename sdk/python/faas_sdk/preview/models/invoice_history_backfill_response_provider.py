from typing import Literal

InvoiceHistoryBackfillResponseProvider = Literal["paddle", "polar", "stripe"]

INVOICE_HISTORY_BACKFILL_RESPONSE_PROVIDER_VALUES: set[InvoiceHistoryBackfillResponseProvider] = {
    "paddle",
    "polar",
    "stripe",
}


def check_invoice_history_backfill_response_provider(value: str) -> InvoiceHistoryBackfillResponseProvider:
    if value in INVOICE_HISTORY_BACKFILL_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {INVOICE_HISTORY_BACKFILL_RESPONSE_PROVIDER_VALUES!r}"
    )
