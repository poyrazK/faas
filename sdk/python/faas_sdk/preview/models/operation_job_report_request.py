from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_report_request import OperationReportRequest


T = TypeVar("T", bound="OperationJobReportRequest")


@_attrs_define
class OperationJobReportRequest:
    """Stable native task report carrying progress or typed private output."""

    report_id: str
    progress: OperationReportRequest | Unset = UNSET
    """Idempotent fenced progress report."""
    result: Any | Unset = UNSET
    """JSON result matching the immutable output schema."""

    def to_dict(self) -> dict[str, Any]:
        report_id = self.report_id

        progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.progress, Unset):
            progress = self.progress.to_dict()

        result = self.result

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "report_id": report_id,
            }
        )
        if progress is not UNSET:
            field_dict["progress"] = progress
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_report_request import OperationReportRequest

        d = dict(src_dict)
        report_id = d.pop("report_id")

        _progress = d.pop("progress", UNSET)
        progress: OperationReportRequest | Unset
        if isinstance(_progress, Unset):
            progress = UNSET
        else:
            progress = OperationReportRequest.from_dict(_progress)

        result = d.pop("result", UNSET)

        operation_job_report_request = cls(
            report_id=report_id,
            progress=progress,
            result=result,
        )

        return operation_job_report_request
