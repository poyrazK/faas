from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OCIHealthcheckTiming")


@_attrs_define
class OCIHealthcheckTiming:
    """Exact OCI image healthcheck durations in nanoseconds, preserved without rounding to whole seconds."""

    interval_ns: int | Unset = UNSET
    timeout_ns: int | Unset = UNSET
    start_period_ns: int | Unset = UNSET
    start_interval_ns: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        interval_ns = self.interval_ns

        timeout_ns = self.timeout_ns

        start_period_ns = self.start_period_ns

        start_interval_ns = self.start_interval_ns

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if interval_ns is not UNSET:
            field_dict["interval_ns"] = interval_ns
        if timeout_ns is not UNSET:
            field_dict["timeout_ns"] = timeout_ns
        if start_period_ns is not UNSET:
            field_dict["start_period_ns"] = start_period_ns
        if start_interval_ns is not UNSET:
            field_dict["start_interval_ns"] = start_interval_ns

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        interval_ns = d.pop("interval_ns", UNSET)

        timeout_ns = d.pop("timeout_ns", UNSET)

        start_period_ns = d.pop("start_period_ns", UNSET)

        start_interval_ns = d.pop("start_interval_ns", UNSET)

        oci_healthcheck_timing = cls(
            interval_ns=interval_ns,
            timeout_ns=timeout_ns,
            start_period_ns=start_period_ns,
            start_interval_ns=start_interval_ns,
        )

        return oci_healthcheck_timing
