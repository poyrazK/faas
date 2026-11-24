from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessOverride")


@_attrs_define
class MCPReadinessOverride:
    """Retained audited readiness override for a deployment."""

    id: UUID
    deployment_id: UUID
    policy_revision: UUID
    artifact_sha256: str
    reason: str
    created_by: UUID
    created_at: datetime.datetime
    expires_at: datetime.datetime
    used_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        deployment_id = str(self.deployment_id)

        policy_revision = str(self.policy_revision)

        artifact_sha256 = self.artifact_sha256

        reason = self.reason

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        used_at: str | Unset = UNSET
        if not isinstance(self.used_at, Unset):
            used_at = self.used_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "deployment_id": deployment_id,
                "policy_revision": policy_revision,
                "artifact_sha256": artifact_sha256,
                "reason": reason,
                "created_by": created_by,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if used_at is not UNSET:
            field_dict["used_at"] = used_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        deployment_id = UUID(d.pop("deployment_id"))

        policy_revision = UUID(d.pop("policy_revision"))

        artifact_sha256 = d.pop("artifact_sha256")

        reason = d.pop("reason")

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _used_at = d.pop("used_at", UNSET)
        used_at: datetime.datetime | Unset
        if isinstance(_used_at, Unset):
            used_at = UNSET
        else:
            used_at = datetime.datetime.fromisoformat(_used_at)

        mcp_readiness_override = cls(
            id=id,
            deployment_id=deployment_id,
            policy_revision=policy_revision,
            artifact_sha256=artifact_sha256,
            reason=reason,
            created_by=created_by,
            created_at=created_at,
            expires_at=expires_at,
            used_at=used_at,
        )

        return mcp_readiness_override
