from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_monitor_signal_state import (
    MCPCatalogMonitorSignalState,
    check_mcp_catalog_monitor_signal_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_monitor_signal_roles import MCPCatalogMonitorSignalRoles


T = TypeVar("T", bound="MCPCatalogMonitorSignal")


@_attrs_define
class MCPCatalogMonitorSignal:
    """Freshness-aware catalog health and evidence links used in monitoring notifications."""

    state: MCPCatalogMonitorSignalState
    changed_count: int
    role: str | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    receipt_id: UUID | Unset = UNSET
    evidence_path: str | Unset = UNSET
    unavailable_roles: list[str] | Unset = UNSET
    drifted_roles: list[str] | Unset = UNSET
    roles: MCPCatalogMonitorSignalRoles | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        changed_count = self.changed_count

        role = self.role

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        receipt_id: str | Unset = UNSET
        if not isinstance(self.receipt_id, Unset):
            receipt_id = str(self.receipt_id)

        evidence_path = self.evidence_path

        unavailable_roles: list[str] | Unset = UNSET
        if not isinstance(self.unavailable_roles, Unset):
            unavailable_roles = self.unavailable_roles

        drifted_roles: list[str] | Unset = UNSET
        if not isinstance(self.drifted_roles, Unset):
            drifted_roles = self.drifted_roles

        roles: dict[str, Any] | Unset = UNSET
        if not isinstance(self.roles, Unset):
            roles = self.roles.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "changed_count": changed_count,
            }
        )
        if role is not UNSET:
            field_dict["role"] = role
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if receipt_id is not UNSET:
            field_dict["receipt_id"] = receipt_id
        if evidence_path is not UNSET:
            field_dict["evidence_path"] = evidence_path
        if unavailable_roles is not UNSET:
            field_dict["unavailable_roles"] = unavailable_roles
        if drifted_roles is not UNSET:
            field_dict["drifted_roles"] = drifted_roles
        if roles is not UNSET:
            field_dict["roles"] = roles

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_monitor_signal_roles import MCPCatalogMonitorSignalRoles

        d = dict(src_dict)
        state = check_mcp_catalog_monitor_signal_state(d.pop("state"))

        changed_count = d.pop("changed_count")

        role = d.pop("role", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _receipt_id = d.pop("receipt_id", UNSET)
        receipt_id: UUID | Unset
        if isinstance(_receipt_id, Unset):
            receipt_id = UNSET
        else:
            receipt_id = UUID(_receipt_id)

        evidence_path = d.pop("evidence_path", UNSET)

        unavailable_roles = cast(list[str], d.pop("unavailable_roles", UNSET))

        drifted_roles = cast(list[str], d.pop("drifted_roles", UNSET))

        _roles = d.pop("roles", UNSET)
        roles: MCPCatalogMonitorSignalRoles | Unset
        if isinstance(_roles, Unset):
            roles = UNSET
        else:
            roles = MCPCatalogMonitorSignalRoles.from_dict(_roles)

        mcp_catalog_monitor_signal = cls(
            state=state,
            changed_count=changed_count,
            role=role,
            checked_at=checked_at,
            receipt_id=receipt_id,
            evidence_path=evidence_path,
            unavailable_roles=unavailable_roles,
            drifted_roles=drifted_roles,
            roles=roles,
        )

        mcp_catalog_monitor_signal.additional_properties = d
        return mcp_catalog_monitor_signal

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
