from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ProjectWorkerJobSample")


@_attrs_define
class ProjectWorkerJobSample:
    """Attributed delivery attempts in a bounded window, including unknown unfinished deliveries."""

    attempts: int
    failures: int
    incomplete: int
    duration_ms: int
    retries: int
    queue_delay_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempts = self.attempts

        failures = self.failures

        incomplete = self.incomplete

        duration_ms = self.duration_ms

        retries = self.retries

        queue_delay_ms = self.queue_delay_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempts": attempts,
                "failures": failures,
                "incomplete": incomplete,
                "duration_ms": duration_ms,
                "retries": retries,
                "queue_delay_ms": queue_delay_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempts = d.pop("attempts")

        failures = d.pop("failures")

        incomplete = d.pop("incomplete")

        duration_ms = d.pop("duration_ms")

        retries = d.pop("retries")

        queue_delay_ms = d.pop("queue_delay_ms")

        project_worker_job_sample = cls(
            attempts=attempts,
            failures=failures,
            incomplete=incomplete,
            duration_ms=duration_ms,
            retries=retries,
            queue_delay_ms=queue_delay_ms,
        )

        project_worker_job_sample.additional_properties = d
        return project_worker_job_sample

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
