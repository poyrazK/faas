from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPContractOverride")


@_attrs_define
class MCPContractOverride:
    """An audited permission for one breaking or unverified candidate."""

    id: UUID
    reason: str
    created_by: str
    created_at: datetime.datetime
    expires_at: datetime.datetime
    deployment_id: UUID | Unset = UNSET
    """The only deployment the override admits, when bound."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        reason = self.reason

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "reason": reason,
                "created_by": created_by,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        reason = d.pop("reason")

        created_by = d.pop("created_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        mcp_contract_override = cls(
            id=id,
            reason=reason,
            created_by=created_by,
            created_at=created_at,
            expires_at=expires_at,
            deployment_id=deployment_id,
        )

        mcp_contract_override.additional_properties = d
        return mcp_contract_override

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
