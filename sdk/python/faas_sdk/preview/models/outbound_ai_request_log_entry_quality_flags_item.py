from typing import Literal

OutboundAIRequestLogEntryQualityFlagsItem = Literal["empty", "invalid_json", "refused", "tool_error", "truncated"]

OUTBOUND_AI_REQUEST_LOG_ENTRY_QUALITY_FLAGS_ITEM_VALUES: set[OutboundAIRequestLogEntryQualityFlagsItem] = {
    "empty",
    "invalid_json",
    "refused",
    "tool_error",
    "truncated",
}


def check_outbound_ai_request_log_entry_quality_flags_item(value: str) -> OutboundAIRequestLogEntryQualityFlagsItem:
    if value in OUTBOUND_AI_REQUEST_LOG_ENTRY_QUALITY_FLAGS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_REQUEST_LOG_ENTRY_QUALITY_FLAGS_ITEM_VALUES!r}"
    )
