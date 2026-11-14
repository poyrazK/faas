from typing import Literal

ToolPublicationRecordEvidenceOrigin = Literal["publisher_attested"]

TOOL_PUBLICATION_RECORD_EVIDENCE_ORIGIN_VALUES: set[ToolPublicationRecordEvidenceOrigin] = {
    "publisher_attested",
}


def check_tool_publication_record_evidence_origin(value: str) -> ToolPublicationRecordEvidenceOrigin:
    if value in TOOL_PUBLICATION_RECORD_EVIDENCE_ORIGIN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_RECORD_EVIDENCE_ORIGIN_VALUES!r}")
