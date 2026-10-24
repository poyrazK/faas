from typing import Literal

DebugTelemetryRequestItemEvidenceStatus = Literal["request_id_only"]

DEBUG_TELEMETRY_REQUEST_ITEM_EVIDENCE_STATUS_VALUES: set[DebugTelemetryRequestItemEvidenceStatus] = {
    "request_id_only",
}


def check_debug_telemetry_request_item_evidence_status(value: str) -> DebugTelemetryRequestItemEvidenceStatus:
    if value in DEBUG_TELEMETRY_REQUEST_ITEM_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEBUG_TELEMETRY_REQUEST_ITEM_EVIDENCE_STATUS_VALUES!r}"
    )
