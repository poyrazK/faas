from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionUsageSummaryResponse")


@_attrs_define
class ExecutionUsageSummaryResponse:
    """Account-level usage roll-up for terminal disposable executions in one UTC calendar month. Payload cleanup does not
    remove these ledger-backed facts.

    """

    runs: int
    wall_time_ms: int
    """Sum of host-measured wall time across terminal runs."""
    cpu_time_ms: int
    """Sum of host-measured CPU time across terminal runs."""
    peak_memory_mb: int
    """Maximum host-measured peak memory across terminal runs."""
    output_bytes: int
    """Sum of result, stdout, and stderr bytes across terminal runs."""
    succeeded: int
    failed: int
    timed_out: int
    out_of_memory: int
    cancelled: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        runs = self.runs

        wall_time_ms = self.wall_time_ms

        cpu_time_ms = self.cpu_time_ms

        peak_memory_mb = self.peak_memory_mb

        output_bytes = self.output_bytes

        succeeded = self.succeeded

        failed = self.failed

        timed_out = self.timed_out

        out_of_memory = self.out_of_memory

        cancelled = self.cancelled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "runs": runs,
                "wall_time_ms": wall_time_ms,
                "cpu_time_ms": cpu_time_ms,
                "peak_memory_mb": peak_memory_mb,
                "output_bytes": output_bytes,
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
        runs = d.pop("runs")

        wall_time_ms = d.pop("wall_time_ms")

        cpu_time_ms = d.pop("cpu_time_ms")

        peak_memory_mb = d.pop("peak_memory_mb")

        output_bytes = d.pop("output_bytes")

        succeeded = d.pop("succeeded")

        failed = d.pop("failed")

        timed_out = d.pop("timed_out")

        out_of_memory = d.pop("out_of_memory")

        cancelled = d.pop("cancelled")

        execution_usage_summary_response = cls(
            runs=runs,
            wall_time_ms=wall_time_ms,
            cpu_time_ms=cpu_time_ms,
            peak_memory_mb=peak_memory_mb,
            output_bytes=output_bytes,
            succeeded=succeeded,
            failed=failed,
            timed_out=timed_out,
            out_of_memory=out_of_memory,
            cancelled=cancelled,
        )

        execution_usage_summary_response.additional_properties = d
        return execution_usage_summary_response

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
