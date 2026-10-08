from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_request_mix_snapshot_status import (
    ProfileRequestMixSnapshotStatus,
    check_profile_request_mix_snapshot_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_request_mix_window import ProfileRequestMixWindow


T = TypeVar("T", bound="ProfileRequestMixSnapshot")


@_attrs_define
class ProfileRequestMixSnapshot:
    """Frozen observed telemetry summary, at most 16 KiB JSON. Completeness describes route/status aggregation, not
    telemetry delivery. Readable with the assessment after raw request telemetry expires.

    """

    captured_at: datetime.datetime
    status: ProfileRequestMixSnapshotStatus
    complete: bool
    reason: str
    warnings: list[str]
    baseline: ProfileRequestMixWindow | Unset = UNSET
    candidate: ProfileRequestMixWindow | Unset = UNSET
    route_difference: float | Unset = UNSET
    """Frozen total variation distance in percentage points; omitted for truncated or unavailable route
    distributions."""
    status_difference: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        captured_at = self.captured_at.isoformat()

        status: str = self.status

        complete = self.complete

        reason = self.reason

        warnings = self.warnings

        baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline, Unset):
            baseline = self.baseline.to_dict()

        candidate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate, Unset):
            candidate = self.candidate.to_dict()

        route_difference = self.route_difference

        status_difference = self.status_difference

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "captured_at": captured_at,
                "status": status,
                "complete": complete,
                "reason": reason,
                "warnings": warnings,
            }
        )
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if candidate is not UNSET:
            field_dict["candidate"] = candidate
        if route_difference is not UNSET:
            field_dict["route_difference"] = route_difference
        if status_difference is not UNSET:
            field_dict["status_difference"] = status_difference

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_request_mix_window import ProfileRequestMixWindow

        d = dict(src_dict)
        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        status = check_profile_request_mix_snapshot_status(d.pop("status"))

        complete = d.pop("complete")

        reason = d.pop("reason")

        warnings = cast(list[str], d.pop("warnings"))

        _baseline = d.pop("baseline", UNSET)
        baseline: ProfileRequestMixWindow | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = ProfileRequestMixWindow.from_dict(_baseline)

        _candidate = d.pop("candidate", UNSET)
        candidate: ProfileRequestMixWindow | Unset
        if isinstance(_candidate, Unset):
            candidate = UNSET
        else:
            candidate = ProfileRequestMixWindow.from_dict(_candidate)

        route_difference = d.pop("route_difference", UNSET)

        status_difference = d.pop("status_difference", UNSET)

        profile_request_mix_snapshot = cls(
            captured_at=captured_at,
            status=status,
            complete=complete,
            reason=reason,
            warnings=warnings,
            baseline=baseline,
            candidate=candidate,
            route_difference=route_difference,
            status_difference=status_difference,
        )

        profile_request_mix_snapshot.additional_properties = d
        return profile_request_mix_snapshot

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
