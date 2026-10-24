from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_item_status import (
    EventExecutionLineageRetryJobItemStatus,
    check_event_execution_lineage_retry_job_item_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_execution_lineage_case import EventExecutionLineageCase
    from ..models.event_execution_lineage_retry_selection import EventExecutionLineageRetrySelection


T = TypeVar("T", bound="EventExecutionLineageRetryJobItem")


@_attrs_define
class EventExecutionLineageRetryJobItem:
    """Retained evaluation status and guarded retry result for one selected case in a bulk job."""

    selection: EventExecutionLineageRetrySelection
    """One reviewed lineage case with its revision and optional diagnostic incarnation guard."""
    status: EventExecutionLineageRetryJobItemStatus
    reason: str | Unset = UNSET
    result: EventExecutionLineageCase | Unset = UNSET
    """Retained reason-coded lineage diagnostic with revision and incarnation guards for reevaluation."""

    def to_dict(self) -> dict[str, Any]:
        selection = self.selection.to_dict()

        status: str = self.status

        reason = self.reason

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "selection": selection,
                "status": status,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_case import EventExecutionLineageCase
        from ..models.event_execution_lineage_retry_selection import EventExecutionLineageRetrySelection

        d = dict(src_dict)
        selection = EventExecutionLineageRetrySelection.from_dict(d.pop("selection"))

        status = check_event_execution_lineage_retry_job_item_status(d.pop("status"))

        reason = d.pop("reason", UNSET)

        _result = d.pop("result", UNSET)
        result: EventExecutionLineageCase | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = EventExecutionLineageCase.from_dict(_result)

        event_execution_lineage_retry_job_item = cls(
            selection=selection,
            status=status,
            reason=reason,
            result=result,
        )

        return event_execution_lineage_retry_job_item
