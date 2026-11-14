from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_tenant_membership import DataPipelineTenantMembership


T = TypeVar("T", bound="DataPipelineTenantAccess")


@_attrs_define
class DataPipelineTenantAccess:
    """Row access through a text, varchar or UUID column present on every selected table. Memberships match verified JWT
    sub values exactly. Empty memberships revoke every reader.

    """

    column: str
    memberships: list[DataPipelineTenantMembership]
    """At most 128 distinct subjects. Tenant and subject values are bounded to 256 UTF-8 bytes."""
    valid_until: datetime.datetime | Unset = UNSET
    """Worker-maintained expiry of the last authoritative Auth membership proof. Expired policies deny SQL reads
    and return HTTP 503."""

    def to_dict(self) -> dict[str, Any]:
        column = self.column

        memberships = []
        for memberships_item_data in self.memberships:
            memberships_item = memberships_item_data.to_dict()
            memberships.append(memberships_item)

        valid_until: str | Unset = UNSET
        if not isinstance(self.valid_until, Unset):
            valid_until = self.valid_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "column": column,
                "memberships": memberships,
            }
        )
        if valid_until is not UNSET:
            field_dict["valid_until"] = valid_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_tenant_membership import DataPipelineTenantMembership

        d = dict(src_dict)
        column = d.pop("column")

        memberships = []
        _memberships = d.pop("memberships")
        for memberships_item_data in _memberships:
            memberships_item = DataPipelineTenantMembership.from_dict(memberships_item_data)

            memberships.append(memberships_item)

        _valid_until = d.pop("valid_until", UNSET)
        valid_until: datetime.datetime | Unset
        if isinstance(_valid_until, Unset):
            valid_until = UNSET
        else:
            valid_until = datetime.datetime.fromisoformat(_valid_until)

        data_pipeline_tenant_access = cls(
            column=column,
            memberships=memberships,
            valid_until=valid_until,
        )

        return data_pipeline_tenant_access
