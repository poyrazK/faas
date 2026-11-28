from typing import Literal

ExportOutboundAITagUsageFormat = Literal["csv", "ndjson"]

EXPORT_OUTBOUND_AI_TAG_USAGE_FORMAT_VALUES: set[ExportOutboundAITagUsageFormat] = {
    "csv",
    "ndjson",
}


def check_export_outbound_ai_tag_usage_format(value: str) -> ExportOutboundAITagUsageFormat:
    if value in EXPORT_OUTBOUND_AI_TAG_USAGE_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXPORT_OUTBOUND_AI_TAG_USAGE_FORMAT_VALUES!r}")
