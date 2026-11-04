from typing import Literal

MCPAdmissionEvidenceOutcome = Literal["accepted", "rejected"]

MCP_ADMISSION_EVIDENCE_OUTCOME_VALUES: set[MCPAdmissionEvidenceOutcome] = {
    "accepted",
    "rejected",
}


def check_mcp_admission_evidence_outcome(value: str) -> MCPAdmissionEvidenceOutcome:
    if value in MCP_ADMISSION_EVIDENCE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_ADMISSION_EVIDENCE_OUTCOME_VALUES!r}")
