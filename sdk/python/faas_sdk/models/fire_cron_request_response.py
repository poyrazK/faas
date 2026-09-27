from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.fire_cron_request_response_status import (
    FireCronRequestResponseStatus,
    check_fire_cron_request_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="FireCronRequestResponse")


@_attrs_define
class FireCronRequestResponse:
    """Read shape for `GET /v1/cron-fire-now-requests/{request_id}`."""

    request_id: UUID
    cron_id: UUID
    status: FireCronRequestResponseStatus
    requested_at: datetime.datetime
    account_id: UUID
    finished_at: datetime.datetime | None | Unset = UNSET
    invocation_id: None | Unset | UUID = UNSET
    task_id: None | Unset | UUID = UNSET
    """Command task created by this request; absent for HTTP crons or before queueing."""
    error: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        cron_id = str(self.cron_id)

        status: str = self.status

        requested_at = self.requested_at.isoformat()

        account_id = str(self.account_id)

        finished_at: None | str | Unset
        if isinstance(self.finished_at, Unset):
            finished_at = UNSET
        elif isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        invocation_id: None | str | Unset
        if isinstance(self.invocation_id, Unset):
            invocation_id = UNSET
        elif isinstance(self.invocation_id, UUID):
            invocation_id = str(self.invocation_id)
        else:
            invocation_id = self.invocation_id

        task_id: None | str | Unset
        if isinstance(self.task_id, Unset):
            task_id = UNSET
        elif isinstance(self.task_id, UUID):
            task_id = str(self.task_id)
        else:
            task_id = self.task_id

        error: None | str | Unset
        if isinstance(self.error, Unset):
            error = UNSET
        else:
            error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "cron_id": cron_id,
                "status": status,
                "requested_at": requested_at,
                "account_id": account_id,
            }
        )
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if task_id is not UNSET:
            field_dict["task_id"] = task_id
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        cron_id = UUID(d.pop("cron_id"))

        status = check_fire_cron_request_response_status(d.pop("status"))

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        account_id = UUID(d.pop("account_id"))

        def _parse_finished_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                finished_at_type_0 = datetime.datetime.fromisoformat(data)

                return finished_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        finished_at = _parse_finished_at(d.pop("finished_at", UNSET))

        def _parse_invocation_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                invocation_id_type_0 = UUID(data)

                return invocation_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        invocation_id = _parse_invocation_id(d.pop("invocation_id", UNSET))

        def _parse_task_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                task_id_type_0 = UUID(data)

                return task_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        task_id = _parse_task_id(d.pop("task_id", UNSET))

        def _parse_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error = _parse_error(d.pop("error", UNSET))

        fire_cron_request_response = cls(
            request_id=request_id,
            cron_id=cron_id,
            status=status,
            requested_at=requested_at,
            account_id=account_id,
            finished_at=finished_at,
            invocation_id=invocation_id,
            task_id=task_id,
            error=error,
        )

        fire_cron_request_response.additional_properties = d
        return fire_cron_request_response

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
