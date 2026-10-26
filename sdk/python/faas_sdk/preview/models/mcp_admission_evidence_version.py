from typing import Literal

MCPAdmissionEvidenceVersion = Literal[1]

MCP_ADMISSION_EVIDENCE_VERSION_VALUES: set[MCPAdmissionEvidenceVersion] = {
    1,
}


def check_mcp_admission_evidence_version(value: int) -> MCPAdmissionEvidenceVersion:
    if value in MCP_ADMISSION_EVIDENCE_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_ADMISSION_EVIDENCE_VERSION_VALUES!r}")
