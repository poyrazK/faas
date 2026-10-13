from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem


T = TypeVar("T", bound="OperationWorkflowPlaybookProgressChange")


@_attrs_define
class OperationWorkflowPlaybookProgressChange:
    """OperationWorkflowPlaybookProgressChange. Durable advisory checklist coordination, independent of application
    business success.

    """

    revision: int
    """OperationWorkflowPlaybookProgressChange.revision. Committed revision after this edit."""
    item_index: int
    """OperationWorkflowPlaybookProgressChange.item_index. Edited frozen checklist position."""
    item: OperationWorkflowPlaybookProgressItem
    """OperationWorkflowPlaybookProgressItem. Durable advisory checklist coordination, independent of application
    business success."""
    at: datetime.datetime
    """OperationWorkflowPlaybookProgressChange.at. Server commit preparation clock."""
    actor: str
    """OperationWorkflowPlaybookProgressChange.actor. Server-derived reviewer identity."""
    outcome_code: str | Unset = UNSET
    """OperationWorkflowPlaybookProgressChange.outcome_code. Present for outcome follow-up step reviews; item_index
    is local to that list."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        item_index = self.item_index

        item = self.item.to_dict()

        at = self.at.isoformat()

        actor = self.actor

        outcome_code = self.outcome_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "item_index": item_index,
                "item": item,
                "at": at,
                "actor": actor,
            }
        )
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem

        d = dict(src_dict)
        revision = d.pop("revision")

        item_index = d.pop("item_index")

        item = OperationWorkflowPlaybookProgressItem.from_dict(d.pop("item"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        outcome_code = d.pop("outcome_code", UNSET)

        operation_workflow_playbook_progress_change = cls(
            revision=revision,
            item_index=item_index,
            item=item,
            at=at,
            actor=actor,
            outcome_code=outcome_code,
        )

        return operation_workflow_playbook_progress_change
