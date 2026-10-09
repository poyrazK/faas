from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowQueuedRunCancelRequest")


@_attrs_define
class WorkflowQueuedRunCancelRequest:
    """Selection of workflow runs for a preview or unstarted cancellation."""

    run_ids: list[UUID]
    workflow_name: str | Unset = UNSET
    """Optional exact workflow-name guard for the selection."""

    def to_dict(self) -> dict[str, Any]:
        run_ids = []
        for run_ids_item_data in self.run_ids:
            run_ids_item = str(run_ids_item_data)
            run_ids.append(run_ids_item)

        workflow_name = self.workflow_name

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "run_ids": run_ids,
            }
        )
        if workflow_name is not UNSET:
            field_dict["workflow_name"] = workflow_name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_ids = []
        _run_ids = d.pop("run_ids")
        for run_ids_item_data in _run_ids:
            run_ids_item = UUID(run_ids_item_data)

            run_ids.append(run_ids_item)

        workflow_name = d.pop("workflow_name", UNSET)

        workflow_queued_run_cancel_request = cls(
            run_ids=run_ids,
            workflow_name=workflow_name,
        )

        return workflow_queued_run_cancel_request
