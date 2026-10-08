from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.crash_capture_response_status import CrashCaptureResponseStatus, check_crash_capture_response_status
from ..models.crash_capture_response_trigger import CrashCaptureResponseTrigger, check_crash_capture_response_trigger
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.crash_capture_response_failure import CrashCaptureResponseFailure


T = TypeVar("T", bound="CrashCaptureResponse")


@_attrs_define
class CrashCaptureResponse:
    """One crash snapshot (ADR-733). `status` moves from `requested` through
    `capturing` to `ready`, then `expired`, or ends as `failed`. Storage
    locations are never returned; open a ready capture as a fork.

    """

    id: UUID
    app_id: UUID
    deployment_id: UUID
    trigger: CrashCaptureResponseTrigger
    status: CrashCaptureResponseStatus
    requested_at: datetime.datetime
    status_code: int | Unset = UNSET
    route: str | Unset = UNSET
    """Request path of the failing request (http_5xx only)."""
    mem_bytes: int | Unset = UNSET
    failure: CrashCaptureResponseFailure | Unset = UNSET
    captured_at: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    """When the capture is deleted."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        trigger: str = self.trigger

        status: str = self.status

        requested_at = self.requested_at.isoformat()

        status_code = self.status_code

        route = self.route

        mem_bytes = self.mem_bytes

        failure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure, Unset):
            failure = self.failure.to_dict()

        captured_at: str | Unset = UNSET
        if not isinstance(self.captured_at, Unset):
            captured_at = self.captured_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "trigger": trigger,
                "status": status,
                "requested_at": requested_at,
            }
        )
        if status_code is not UNSET:
            field_dict["status_code"] = status_code
        if route is not UNSET:
            field_dict["route"] = route
        if mem_bytes is not UNSET:
            field_dict["mem_bytes"] = mem_bytes
        if failure is not UNSET:
            field_dict["failure"] = failure
        if captured_at is not UNSET:
            field_dict["captured_at"] = captured_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.crash_capture_response_failure import CrashCaptureResponseFailure

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        trigger = check_crash_capture_response_trigger(d.pop("trigger"))

        status = check_crash_capture_response_status(d.pop("status"))

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        status_code = d.pop("status_code", UNSET)

        route = d.pop("route", UNSET)

        mem_bytes = d.pop("mem_bytes", UNSET)

        _failure = d.pop("failure", UNSET)
        failure: CrashCaptureResponseFailure | Unset
        if isinstance(_failure, Unset):
            failure = UNSET
        else:
            failure = CrashCaptureResponseFailure.from_dict(_failure)

        _captured_at = d.pop("captured_at", UNSET)
        captured_at: datetime.datetime | Unset
        if isinstance(_captured_at, Unset):
            captured_at = UNSET
        else:
            captured_at = datetime.datetime.fromisoformat(_captured_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        crash_capture_response = cls(
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            trigger=trigger,
            status=status,
            requested_at=requested_at,
            status_code=status_code,
            route=route,
            mem_bytes=mem_bytes,
            failure=failure,
            captured_at=captured_at,
            expires_at=expires_at,
        )

        crash_capture_response.additional_properties = d
        return crash_capture_response

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
