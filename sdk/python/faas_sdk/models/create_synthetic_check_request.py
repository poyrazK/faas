from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_synthetic_check_request_interval_seconds import (
    CreateSyntheticCheckRequestIntervalSeconds,
    check_create_synthetic_check_request_interval_seconds,
)
from ..models.create_synthetic_check_request_method import (
    CreateSyntheticCheckRequestMethod,
    check_create_synthetic_check_request_method,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateSyntheticCheckRequest")


@_attrs_define
class CreateSyntheticCheckRequest:
    """Defines a scheduled HTTP check against the app (ADR-748)."""

    name: str
    """Unique per app."""
    path: str
    """Origin-relative path, query string allowed. The host is always the app's own default hostname."""
    interval_seconds: CreateSyntheticCheckRequestIntervalSeconds
    """How often the check runs. Five minutes is the floor so a check never keeps an app permanently awake."""
    method: CreateSyntheticCheckRequestMethod | Unset = "GET"
    """HTTP method of the probe."""
    expected_status: int | Unset = UNSET
    """Exact status that counts as success; omit to accept any 2xx. Redirects are not followed."""
    timeout_ms: int | Unset = 10000
    """Whole-request timeout, including any wake of a parked app."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        path = self.path

        interval_seconds: int = self.interval_seconds

        method: str | Unset = UNSET
        if not isinstance(self.method, Unset):
            method = self.method

        expected_status = self.expected_status

        timeout_ms = self.timeout_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "path": path,
                "interval_seconds": interval_seconds,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
        if expected_status is not UNSET:
            field_dict["expected_status"] = expected_status
        if timeout_ms is not UNSET:
            field_dict["timeout_ms"] = timeout_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        path = d.pop("path")

        interval_seconds = check_create_synthetic_check_request_interval_seconds(d.pop("interval_seconds"))

        _method = d.pop("method", UNSET)
        method: CreateSyntheticCheckRequestMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_create_synthetic_check_request_method(_method)

        expected_status = d.pop("expected_status", UNSET)

        timeout_ms = d.pop("timeout_ms", UNSET)

        create_synthetic_check_request = cls(
            name=name,
            path=path,
            interval_seconds=interval_seconds,
            method=method,
            expected_status=expected_status,
            timeout_ms=timeout_ms,
        )

        create_synthetic_check_request.additional_properties = d
        return create_synthetic_check_request

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
