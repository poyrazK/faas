from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.job_run_response import JobRunResponse


T = TypeVar("T", bound="JobRunCancelledResponse")


@_attrs_define
class JobRunCancelledResponse:
    """POST .../runs/{id}/cancel body. Returns the post-cancel run aggregate + cancelled_at timestamp."""

    run: JobRunResponse
    """Wire projection of state.JobRun. Aggregate counters are recomputed by schedd after every terminal task
    transition."""
    cancelled_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run = self.run.to_dict()

        cancelled_at = self.cancelled_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run": run,
                "cancelled_at": cancelled_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_run_response import JobRunResponse

        d = dict(src_dict)
        run = JobRunResponse.from_dict(d.pop("run"))

        cancelled_at = datetime.datetime.fromisoformat(d.pop("cancelled_at"))

        job_run_cancelled_response = cls(
            run=run,
            cancelled_at=cancelled_at,
        )

        job_run_cancelled_response.additional_properties = d
        return job_run_cancelled_response

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
