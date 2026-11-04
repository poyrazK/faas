from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_digest_check_item import OperationWorkflowNextActionDigestCheckItem


T = TypeVar("T", bound="OperationWorkflowNextActionDigestCheck")


@_attrs_define
class OperationWorkflowNextActionDigestCheck:
    """Read-only current source reconciliation of a saved digest, using independent retained source reads. No delivery
    snapshot or workflow is modified.

    """

    items: list[OperationWorkflowNextActionDigestCheckItem]
    retention_limited: bool

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        retention_limited = self.retention_limited

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "retention_limited": retention_limited,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_digest_check_item import OperationWorkflowNextActionDigestCheckItem

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowNextActionDigestCheckItem.from_dict(items_item_data)

            items.append(items_item)

        retention_limited = d.pop("retention_limited")

        operation_workflow_next_action_digest_check = cls(
            items=items,
            retention_limited=retention_limited,
        )

        return operation_workflow_next_action_digest_check
