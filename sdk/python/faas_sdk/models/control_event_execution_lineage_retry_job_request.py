from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ControlEventExecutionLineageRetryJobRequest")


@_attrs_define
class ControlEventExecutionLineageRetryJobRequest:
    expected_created_at: datetime.datetime
    """Original nonzero creation timestamp from the reviewed job."""

    def to_dict(self) -> dict[str, Any]:
        expected_created_at = self.expected_created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_created_at": expected_created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_created_at = datetime.datetime.fromisoformat(d.pop("expected_created_at"))

        control_event_execution_lineage_retry_job_request = cls(
            expected_created_at=expected_created_at,
        )

        return control_event_execution_lineage_retry_job_request
