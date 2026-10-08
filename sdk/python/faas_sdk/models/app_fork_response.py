from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_fork_response_status import AppForkResponseStatus, check_app_fork_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_fork_response_failure import AppForkResponseFailure


T = TypeVar("T", bound="AppForkResponse")


@_attrs_define
class AppForkResponse:
    """One production fork (ADR-732). `status` moves from `queued` through
    `restoring` to `running`, and ends as `expired`, `cancelled` or
    `failed`. Scheduler leases and instance identifiers are never
    returned.

    """

    id: UUID
    app_id: UUID
    deployment_id: UUID
    """The live deployment pinned when the fork was requested."""
    status: AppForkResponseStatus
    ttl_seconds: int
    expires_at: datetime.datetime
    """When the fork is destroyed (created_at + ttl_seconds)."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    access_token: str | Unset = UNSET
    """Returned only by `POST /v1/apps/{slug}/forks`, never again. To
    reach the running fork, send a request to the app's hostname with
    `X-Gregale-Fork: <id>` and `X-Gregale-Fork-Token: <access_token>`.
    The gateway routes it to the fork, strips both headers, and never
    wakes the app for it.
    """
    cancel_requested_at: datetime.datetime | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    failure: AppForkResponseFailure | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        status: str = self.status

        ttl_seconds = self.ttl_seconds

        expires_at = self.expires_at.isoformat()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        access_token = self.access_token

        cancel_requested_at: str | Unset = UNSET
        if not isinstance(self.cancel_requested_at, Unset):
            cancel_requested_at = self.cancel_requested_at.isoformat()

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        failure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure, Unset):
            failure = self.failure.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "status": status,
                "ttl_seconds": ttl_seconds,
                "expires_at": expires_at,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if access_token is not UNSET:
            field_dict["access_token"] = access_token
        if cancel_requested_at is not UNSET:
            field_dict["cancel_requested_at"] = cancel_requested_at
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if failure is not UNSET:
            field_dict["failure"] = failure

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_fork_response_failure import AppForkResponseFailure

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        status = check_app_fork_response_status(d.pop("status"))

        ttl_seconds = d.pop("ttl_seconds")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        access_token = d.pop("access_token", UNSET)

        _cancel_requested_at = d.pop("cancel_requested_at", UNSET)
        cancel_requested_at: datetime.datetime | Unset
        if isinstance(_cancel_requested_at, Unset):
            cancel_requested_at = UNSET
        else:
            cancel_requested_at = datetime.datetime.fromisoformat(_cancel_requested_at)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        _failure = d.pop("failure", UNSET)
        failure: AppForkResponseFailure | Unset
        if isinstance(_failure, Unset):
            failure = UNSET
        else:
            failure = AppForkResponseFailure.from_dict(_failure)

        app_fork_response = cls(
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            status=status,
            ttl_seconds=ttl_seconds,
            expires_at=expires_at,
            created_at=created_at,
            updated_at=updated_at,
            access_token=access_token,
            cancel_requested_at=cancel_requested_at,
            started_at=started_at,
            finished_at=finished_at,
            failure=failure,
        )

        app_fork_response.additional_properties = d
        return app_fork_response

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
