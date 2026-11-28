from typing import Literal

ToolRegistryToolEvidenceOrigin = Literal["publisher_attested"]

TOOL_REGISTRY_TOOL_EVIDENCE_ORIGIN_VALUES: set[ToolRegistryToolEvidenceOrigin] = {
    "publisher_attested",
}


def check_tool_registry_tool_evidence_origin(value: str) -> ToolRegistryToolEvidenceOrigin:
    if value in TOOL_REGISTRY_TOOL_EVIDENCE_ORIGIN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_REGISTRY_TOOL_EVIDENCE_ORIGIN_VALUES!r}")
