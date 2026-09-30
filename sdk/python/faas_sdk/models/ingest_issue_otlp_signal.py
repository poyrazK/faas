from typing import Literal

IngestIssueOTLPSignal = Literal["logs", "traces"]

INGEST_ISSUE_OTLP_SIGNAL_VALUES: set[IngestIssueOTLPSignal] = {
    "logs",
    "traces",
}


def check_ingest_issue_otlp_signal(value: str) -> IngestIssueOTLPSignal:
    if value in INGEST_ISSUE_OTLP_SIGNAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INGEST_ISSUE_OTLP_SIGNAL_VALUES!r}")
