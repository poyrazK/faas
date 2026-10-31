from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionWorkflowStatusCounts")


@_attrs_define
class ExecutionWorkflowStatusCounts:
    """Number of runs in each lifecycle state."""

    queued: int
    restoring: int
    running: int
    succeeded: int
    failed: int
    timed_out: int
    out_of_memory: int
    cancelled: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queued = self.queued

        restoring = self.restoring

        running = self.running

        succeeded = self.succeeded

        failed = self.failed

        timed_out = self.timed_out

        out_of_memory = self.out_of_memory

        cancelled = self.cancelled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "queued": queued,
                "restoring": restoring,
                "running": running,
                "succeeded": succeeded,
                "failed": failed,
                "timed_out": timed_out,
                "out_of_memory": out_of_memory,
                "cancelled": cancelled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        queued = d.pop("queued")

        restoring = d.pop("restoring")

        running = d.pop("running")

        succeeded = d.pop("succeeded")

        failed = d.pop("failed")

        timed_out = d.pop("timed_out")

        out_of_memory = d.pop("out_of_memory")

        cancelled = d.pop("cancelled")

        execution_workflow_status_counts = cls(
            queued=queued,
            restoring=restoring,
            running=running,
            succeeded=succeeded,
            failed=failed,
            timed_out=timed_out,
            out_of_memory=out_of_memory,
            cancelled=cancelled,
        )

        execution_workflow_status_counts.additional_properties = d
        return execution_workflow_status_counts

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
