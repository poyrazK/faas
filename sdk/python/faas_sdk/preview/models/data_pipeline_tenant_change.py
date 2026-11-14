from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_tenant_change_state import (
    DataPipelineTenantChangeState,
    check_data_pipeline_tenant_change_state,
)

if TYPE_CHECKING:
    from ..models.data_pipeline_tenant_membership import DataPipelineTenantMembership


T = TypeVar("T", bound="DataPipelineTenantChange")


@_attrs_define
class DataPipelineTenantChange:
    """Frozen membership preview. Approval applies this exact after set atomically at the mirror before catalog
    acknowledgement.

    """

    id: str
    state: DataPipelineTenantChangeState
    preview_revision: int
    before: list[DataPipelineTenantMembership]
    after: list[DataPipelineTenantMembership]

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        state: str = self.state

        preview_revision = self.preview_revision

        before = []
        for before_item_data in self.before:
            before_item = before_item_data.to_dict()
            before.append(before_item)

        after = []
        for after_item_data in self.after:
            after_item = after_item_data.to_dict()
            after.append(after_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "state": state,
                "preview_revision": preview_revision,
                "before": before,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_tenant_membership import DataPipelineTenantMembership

        d = dict(src_dict)
        id = d.pop("id")

        state = check_data_pipeline_tenant_change_state(d.pop("state"))

        preview_revision = d.pop("preview_revision")

        before = []
        _before = d.pop("before")
        for before_item_data in _before:
            before_item = DataPipelineTenantMembership.from_dict(before_item_data)

            before.append(before_item)

        after = []
        _after = d.pop("after")
        for after_item_data in _after:
            after_item = DataPipelineTenantMembership.from_dict(after_item_data)

            after.append(after_item)

        data_pipeline_tenant_change = cls(
            id=id,
            state=state,
            preview_revision=preview_revision,
            before=before,
            after=after,
        )

        return data_pipeline_tenant_change
