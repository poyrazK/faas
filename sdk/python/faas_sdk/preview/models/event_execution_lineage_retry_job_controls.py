from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_execution_lineage_retry_job_control_receipt import EventExecutionLineageRetryJobControlReceipt


T = TypeVar("T", bound="EventExecutionLineageRetryJobControls")


@_attrs_define
class EventExecutionLineageRetryJobControls:
    """Observed scoped page of retained bulk-job control receipts and its continuation cursor."""

    controls: list[EventExecutionLineageRetryJobControlReceipt]
    next_after: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        controls = []
        for controls_item_data in self.controls:
            controls_item = controls_item_data.to_dict()
            controls.append(controls_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "controls": controls,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_retry_job_control_receipt import (
            EventExecutionLineageRetryJobControlReceipt,
        )

        d = dict(src_dict)
        controls = []
        _controls = d.pop("controls")
        for controls_item_data in _controls:
            controls_item = EventExecutionLineageRetryJobControlReceipt.from_dict(controls_item_data)

            controls.append(controls_item)

        next_after = d.pop("next_after", UNSET)

        event_execution_lineage_retry_job_controls = cls(
            controls=controls,
            next_after=next_after,
        )

        return event_execution_lineage_retry_job_controls
