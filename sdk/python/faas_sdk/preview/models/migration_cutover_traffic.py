from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.migration_cutover_traffic_coverage import (
    MigrationCutoverTrafficCoverage,
    check_migration_cutover_traffic_coverage,
)
from ..models.migration_cutover_traffic_status import (
    MigrationCutoverTrafficStatus,
    check_migration_cutover_traffic_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MigrationCutoverTraffic")


@_attrs_define
class MigrationCutoverTraffic:
    """Existing deployment and exact GET route observations after hostname proof. Aggregates are not hostname-specific and
    never prove every customer moved.

    """

    status: MigrationCutoverTrafficStatus
    coverage: MigrationCutoverTrafficCoverage
    reason: str
    requests: int
    server_errors: int
    worst_window_p95_ms: float | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        coverage: str = self.coverage

        reason = self.reason

        requests = self.requests

        server_errors = self.server_errors

        worst_window_p95_ms = self.worst_window_p95_ms

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "coverage": coverage,
                "reason": reason,
                "requests": requests,
                "server_errors": server_errors,
            }
        )
        if worst_window_p95_ms is not UNSET:
            field_dict["worst_window_p95_ms"] = worst_window_p95_ms
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_migration_cutover_traffic_status(d.pop("status"))

        coverage = check_migration_cutover_traffic_coverage(d.pop("coverage"))

        reason = d.pop("reason")

        requests = d.pop("requests")

        server_errors = d.pop("server_errors")

        worst_window_p95_ms = d.pop("worst_window_p95_ms", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        migration_cutover_traffic = cls(
            status=status,
            coverage=coverage,
            reason=reason,
            requests=requests,
            server_errors=server_errors,
            worst_window_p95_ms=worst_window_p95_ms,
            checked_at=checked_at,
        )

        migration_cutover_traffic.additional_properties = d
        return migration_cutover_traffic

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
