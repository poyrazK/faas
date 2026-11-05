from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SweepStuckBuildsResponse")


@_attrs_define
class SweepStuckBuildsResponse:
    """Wire shape for POST /v1/admin/builds/sweep-stuck
    (admin scope + FAAS_ADMIN_EMAILS allowlist). The audit
    row is emitted under operator.action.reclaim_build with
    account_id=NULL (fleet-level sweep, not tenant-scoped).

    """

    ok: bool
    swept_count: int
    """Rows flipped from 'running' to 'failed' with failure_class='timeout'. 0 when none match."""
    older_than_seconds: int
    """Effective threshold after parsing ?older_than=. Clamped to [60, 3600]."""
    threshold_iso: datetime.datetime
    """RFC 3339 cutoff timestamp (now - older_than)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ok = self.ok

        swept_count = self.swept_count

        older_than_seconds = self.older_than_seconds

        threshold_iso = self.threshold_iso.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ok": ok,
                "swept_count": swept_count,
                "older_than_seconds": older_than_seconds,
                "threshold_iso": threshold_iso,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ok = d.pop("ok")

        swept_count = d.pop("swept_count")

        older_than_seconds = d.pop("older_than_seconds")

        threshold_iso = datetime.datetime.fromisoformat(d.pop("threshold_iso"))

        sweep_stuck_builds_response = cls(
            ok=ok,
            swept_count=swept_count,
            older_than_seconds=older_than_seconds,
            threshold_iso=threshold_iso,
        )

        sweep_stuck_builds_response.additional_properties = d
        return sweep_stuck_builds_response

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
