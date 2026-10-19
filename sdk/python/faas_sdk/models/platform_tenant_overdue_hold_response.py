from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PlatformTenantOverdueHoldResponse")


@_attrs_define
class PlatformTenantOverdueHoldResponse:
    """A platform tenant the gateway refuses because of an overdue invoice."""

    tenant_id: UUID
    export_id: UUID
    """The oldest overdue invoice export holding the tenant."""
    due_at: datetime.datetime
    since: datetime.datetime
    """When the hold was placed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        export_id = str(self.export_id)

        due_at = self.due_at.isoformat()

        since = self.since.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "export_id": export_id,
                "due_at": due_at,
                "since": since,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        export_id = UUID(d.pop("export_id"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        since = datetime.datetime.fromisoformat(d.pop("since"))

        platform_tenant_overdue_hold_response = cls(
            tenant_id=tenant_id,
            export_id=export_id,
            due_at=due_at,
            since=since,
        )

        platform_tenant_overdue_hold_response.additional_properties = d
        return platform_tenant_overdue_hold_response

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
