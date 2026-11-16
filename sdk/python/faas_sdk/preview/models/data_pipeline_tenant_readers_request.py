from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_tenant_readers_request_action import (
    DataPipelineTenantReadersRequestAction,
    check_data_pipeline_tenant_readers_request_action,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_tenant_membership import DataPipelineTenantMembership


T = TypeVar("T", bound="DataPipelineTenantReadersRequest")


@_attrs_define
class DataPipelineTenantReadersRequest:
    """Account-owner-only, revision-fenced membership preview, approval or discard. Preview supplies the complete
    replacement membership array. Approve supplies only the saved change_id; grace permits strict membership reductions
    only.

    """

    expected_revision: int
    action: DataPipelineTenantReadersRequestAction
    change_id: str | Unset = UNSET
    memberships: list[DataPipelineTenantMembership] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        action: str = self.action

        change_id = self.change_id

        memberships: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.memberships, Unset):
            memberships = []
            for memberships_item_data in self.memberships:
                memberships_item = memberships_item_data.to_dict()
                memberships.append(memberships_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "action": action,
            }
        )
        if change_id is not UNSET:
            field_dict["change_id"] = change_id
        if memberships is not UNSET:
            field_dict["memberships"] = memberships

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_tenant_membership import DataPipelineTenantMembership

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        action = check_data_pipeline_tenant_readers_request_action(d.pop("action"))

        change_id = d.pop("change_id", UNSET)

        _memberships = d.pop("memberships", UNSET)
        memberships: list[DataPipelineTenantMembership] | Unset = UNSET
        if _memberships is not UNSET:
            memberships = []
            for memberships_item_data in _memberships:
                memberships_item = DataPipelineTenantMembership.from_dict(memberships_item_data)

                memberships.append(memberships_item)

        data_pipeline_tenant_readers_request = cls(
            expected_revision=expected_revision,
            action=action,
            change_id=change_id,
            memberships=memberships,
        )

        return data_pipeline_tenant_readers_request
