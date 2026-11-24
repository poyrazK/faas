from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_admission_evidence_outcome import MCPAdmissionEvidenceOutcome, check_mcp_admission_evidence_outcome
from ..models.mcp_admission_evidence_version import MCPAdmissionEvidenceVersion, check_mcp_admission_evidence_version
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_admission_gateway import MCPAdmissionGateway


T = TypeVar("T", bound="MCPAdmissionEvidence")


@_attrs_define
class MCPAdmissionEvidence:
    """Version 1 durable traffic admission snapshot in deployment audit data.mcp_admission. Accepted commits with traffic;
    rejected is saved after rollback. Existing deployment audit retention applies. This is historical admission
    evidence, not a lease or live authorization proof.

    """

    version: MCPAdmissionEvidenceVersion
    id: UUID
    deployment_id: UUID
    traffic_percent: int
    captured_at: datetime.datetime
    outcome: MCPAdmissionEvidenceOutcome
    expected_fingerprint: str
    gateways: list[MCPAdmissionGateway]
    reason: str | Unset = UNSET
    """Public rejection code, or traffic_update_failed for a later traffic failure. Raw errors are never stored."""
    observed_fingerprint: str | Unset = UNSET
    expected_revision: int | Unset = UNSET
    observed_revision: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        id = str(self.id)

        deployment_id = str(self.deployment_id)

        traffic_percent = self.traffic_percent

        captured_at = self.captured_at.isoformat()

        outcome: str = self.outcome

        expected_fingerprint = self.expected_fingerprint

        gateways = []
        for gateways_item_data in self.gateways:
            gateways_item = gateways_item_data.to_dict()
            gateways.append(gateways_item)

        reason = self.reason

        observed_fingerprint = self.observed_fingerprint

        expected_revision = self.expected_revision

        observed_revision = self.observed_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "id": id,
                "deployment_id": deployment_id,
                "traffic_percent": traffic_percent,
                "captured_at": captured_at,
                "outcome": outcome,
                "expected_fingerprint": expected_fingerprint,
                "gateways": gateways,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if observed_fingerprint is not UNSET:
            field_dict["observed_fingerprint"] = observed_fingerprint
        if expected_revision is not UNSET:
            field_dict["expected_revision"] = expected_revision
        if observed_revision is not UNSET:
            field_dict["observed_revision"] = observed_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_admission_gateway import MCPAdmissionGateway

        d = dict(src_dict)
        version = check_mcp_admission_evidence_version(d.pop("version"))

        id = UUID(d.pop("id"))

        deployment_id = UUID(d.pop("deployment_id"))

        traffic_percent = d.pop("traffic_percent")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        outcome = check_mcp_admission_evidence_outcome(d.pop("outcome"))

        expected_fingerprint = d.pop("expected_fingerprint")

        gateways = []
        _gateways = d.pop("gateways")
        for gateways_item_data in _gateways:
            gateways_item = MCPAdmissionGateway.from_dict(gateways_item_data)

            gateways.append(gateways_item)

        reason = d.pop("reason", UNSET)

        observed_fingerprint = d.pop("observed_fingerprint", UNSET)

        expected_revision = d.pop("expected_revision", UNSET)

        observed_revision = d.pop("observed_revision", UNSET)

        mcp_admission_evidence = cls(
            version=version,
            id=id,
            deployment_id=deployment_id,
            traffic_percent=traffic_percent,
            captured_at=captured_at,
            outcome=outcome,
            expected_fingerprint=expected_fingerprint,
            gateways=gateways,
            reason=reason,
            observed_fingerprint=observed_fingerprint,
            expected_revision=expected_revision,
            observed_revision=observed_revision,
        )

        mcp_admission_evidence.additional_properties = d
        return mcp_admission_evidence

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
