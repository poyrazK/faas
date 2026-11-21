from typing import Literal

ToolRegistryVersionEvidenceOrigin = Literal["publisher_attested"]

TOOL_REGISTRY_VERSION_EVIDENCE_ORIGIN_VALUES: set[ToolRegistryVersionEvidenceOrigin] = {
    "publisher_attested",
}


def check_tool_registry_version_evidence_origin(value: str) -> ToolRegistryVersionEvidenceOrigin:
    if value in TOOL_REGISTRY_VERSION_EVIDENCE_ORIGIN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_REGISTRY_VERSION_EVIDENCE_ORIGIN_VALUES!r}")
