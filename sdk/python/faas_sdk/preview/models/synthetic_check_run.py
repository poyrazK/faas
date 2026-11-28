from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.synthetic_check_run_error_class import SyntheticCheckRunErrorClass, check_synthetic_check_run_error_class
from ..types import UNSET, Unset

T = TypeVar("T", bound="SyntheticCheckRun")


@_attrs_define
class SyntheticCheckRun:
    """One synthetic check probe."""

    started_at: datetime.datetime
    ok: bool
    latency_ms: int
    status_code: int | Unset = UNSET
    """HTTP status received; absent when no response arrived."""
    error_class: SyntheticCheckRunErrorClass | Unset = UNSET
    """Why the run failed; absent on success. status means a response arrived with an unexpected code."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        started_at = self.started_at.isoformat()

        ok = self.ok

        latency_ms = self.latency_ms

        status_code = self.status_code

        error_class: str | Unset = UNSET
        if not isinstance(self.error_class, Unset):
            error_class = self.error_class

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "started_at": started_at,
                "ok": ok,
                "latency_ms": latency_ms,
            }
        )
        if status_code is not UNSET:
            field_dict["status_code"] = status_code
        if error_class is not UNSET:
            field_dict["error_class"] = error_class

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        ok = d.pop("ok")

        latency_ms = d.pop("latency_ms")

        status_code = d.pop("status_code", UNSET)

        _error_class = d.pop("error_class", UNSET)
        error_class: SyntheticCheckRunErrorClass | Unset
        if isinstance(_error_class, Unset):
            error_class = UNSET
        else:
            error_class = check_synthetic_check_run_error_class(_error_class)

        synthetic_check_run = cls(
            started_at=started_at,
            ok=ok,
            latency_ms=latency_ms,
            status_code=status_code,
            error_class=error_class,
        )

        synthetic_check_run.additional_properties = d
        return synthetic_check_run

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
