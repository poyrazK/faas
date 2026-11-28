from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.mcp_readiness_expectation_evidence_ids import MCPReadinessExpectationEvidenceIds


T = TypeVar("T", bound="MCPReadinessExpectation")


@_attrs_define
class MCPReadinessExpectation:
    """Exact fresh role observations required in the traffic transaction, independently of emergency overrides. Mismatches
    return 409 without changing traffic.

    """

    policy_revision: UUID
    artifact_sha256: str
    evidence_ids: MCPReadinessExpectationEvidenceIds
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy_revision = str(self.policy_revision)

        artifact_sha256 = self.artifact_sha256

        evidence_ids = self.evidence_ids.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy_revision": policy_revision,
                "artifact_sha256": artifact_sha256,
                "evidence_ids": evidence_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_expectation_evidence_ids import MCPReadinessExpectationEvidenceIds

        d = dict(src_dict)
        policy_revision = UUID(d.pop("policy_revision"))

        artifact_sha256 = d.pop("artifact_sha256")

        evidence_ids = MCPReadinessExpectationEvidenceIds.from_dict(d.pop("evidence_ids"))

        mcp_readiness_expectation = cls(
            policy_revision=policy_revision,
            artifact_sha256=artifact_sha256,
            evidence_ids=evidence_ids,
        )

        mcp_readiness_expectation.additional_properties = d
        return mcp_readiness_expectation

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
