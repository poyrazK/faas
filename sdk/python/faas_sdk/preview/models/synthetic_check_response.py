from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.synthetic_check_response_interval_seconds import (
    SyntheticCheckResponseIntervalSeconds,
    check_synthetic_check_response_interval_seconds,
)
from ..models.synthetic_check_response_method import SyntheticCheckResponseMethod, check_synthetic_check_response_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.synthetic_check_results import SyntheticCheckResults


T = TypeVar("T", bound="SyntheticCheckResponse")


@_attrs_define
class SyntheticCheckResponse:
    """One synthetic check definition (ADR-748)."""

    id: UUID
    name: str
    method: SyntheticCheckResponseMethod
    path: str
    url: str
    """The full URL each probe requests."""
    timeout_ms: int
    interval_seconds: SyntheticCheckResponseIntervalSeconds
    enabled: bool
    created_at: datetime.datetime
    expected_status: int | Unset = UNSET
    """Exact expected status; absent means any 2xx."""
    results: SyntheticCheckResults | Unset = UNSET
    """Recent outcomes of one synthetic check, returned by GET /v1/apps/{slug}/synthetics/{id} (ADR-748)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        method: str = self.method

        path = self.path

        url = self.url

        timeout_ms = self.timeout_ms

        interval_seconds: int = self.interval_seconds

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        expected_status = self.expected_status

        results: dict[str, Any] | Unset = UNSET
        if not isinstance(self.results, Unset):
            results = self.results.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "method": method,
                "path": path,
                "url": url,
                "timeout_ms": timeout_ms,
                "interval_seconds": interval_seconds,
                "enabled": enabled,
                "created_at": created_at,
            }
        )
        if expected_status is not UNSET:
            field_dict["expected_status"] = expected_status
        if results is not UNSET:
            field_dict["results"] = results

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.synthetic_check_results import SyntheticCheckResults

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        method = check_synthetic_check_response_method(d.pop("method"))

        path = d.pop("path")

        url = d.pop("url")

        timeout_ms = d.pop("timeout_ms")

        interval_seconds = check_synthetic_check_response_interval_seconds(d.pop("interval_seconds"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expected_status = d.pop("expected_status", UNSET)

        _results = d.pop("results", UNSET)
        results: SyntheticCheckResults | Unset
        if isinstance(_results, Unset):
            results = UNSET
        else:
            results = SyntheticCheckResults.from_dict(_results)

        synthetic_check_response = cls(
            id=id,
            name=name,
            method=method,
            path=path,
            url=url,
            timeout_ms=timeout_ms,
            interval_seconds=interval_seconds,
            enabled=enabled,
            created_at=created_at,
            expected_status=expected_status,
            results=results,
        )

        synthetic_check_response.additional_properties = d
        return synthetic_check_response

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
