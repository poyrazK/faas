from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_check import MCPReadinessCheck


T = TypeVar("T", bound="MCPReadinessEvidence")


@_attrs_define
class MCPReadinessEvidence:
    """Platform-recorded readiness results bound to deployment configuration and probe policy."""

    id: UUID
    deployment_id: UUID
    role: str
    policy_revision: UUID
    artifact_sha256: str
    protocol_version: str
    catalog_format: int
    passed: bool
    observed_at: datetime.datetime
    checks: list[MCPReadinessCheck]
    catalog_sha256: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        deployment_id = str(self.deployment_id)

        role = self.role

        policy_revision = str(self.policy_revision)

        artifact_sha256 = self.artifact_sha256

        protocol_version = self.protocol_version

        catalog_format = self.catalog_format

        passed = self.passed

        observed_at = self.observed_at.isoformat()

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        catalog_sha256 = self.catalog_sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "deployment_id": deployment_id,
                "role": role,
                "policy_revision": policy_revision,
                "artifact_sha256": artifact_sha256,
                "protocol_version": protocol_version,
                "catalog_format": catalog_format,
                "passed": passed,
                "observed_at": observed_at,
                "checks": checks,
            }
        )
        if catalog_sha256 is not UNSET:
            field_dict["catalog_sha256"] = catalog_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_check import MCPReadinessCheck

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        deployment_id = UUID(d.pop("deployment_id"))

        role = d.pop("role")

        policy_revision = UUID(d.pop("policy_revision"))

        artifact_sha256 = d.pop("artifact_sha256")

        protocol_version = d.pop("protocol_version")

        catalog_format = d.pop("catalog_format")

        passed = d.pop("passed")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = MCPReadinessCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        catalog_sha256 = d.pop("catalog_sha256", UNSET)

        mcp_readiness_evidence = cls(
            id=id,
            deployment_id=deployment_id,
            role=role,
            policy_revision=policy_revision,
            artifact_sha256=artifact_sha256,
            protocol_version=protocol_version,
            catalog_format=catalog_format,
            passed=passed,
            observed_at=observed_at,
            checks=checks,
            catalog_sha256=catalog_sha256,
        )

        return mcp_readiness_evidence
