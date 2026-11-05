from typing import Literal

ExportOutboundIntegrationAIUserUsageFormat = Literal["csv", "ndjson"]

EXPORT_OUTBOUND_INTEGRATION_AI_USER_USAGE_FORMAT_VALUES: set[ExportOutboundIntegrationAIUserUsageFormat] = {
    "csv",
    "ndjson",
}


def check_export_outbound_integration_ai_user_usage_format(value: str) -> ExportOutboundIntegrationAIUserUsageFormat:
    if value in EXPORT_OUTBOUND_INTEGRATION_AI_USER_USAGE_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXPORT_OUTBOUND_INTEGRATION_AI_USER_USAGE_FORMAT_VALUES!r}"
    )
