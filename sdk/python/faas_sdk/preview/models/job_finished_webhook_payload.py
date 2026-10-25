from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="JobFinishedWebhookPayload")


@_attrs_define
class JobFinishedWebhookPayload:
    """Terminal job details delivered with job.finished."""

    status: str
    duration_ms: int
    job_id: str | Unset = UNSET
    run_id: str | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    account_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status = self.status

        duration_ms = self.duration_ms

        job_id = self.job_id

        run_id = self.run_id

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "duration_ms": duration_ms,
            }
        )
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if account_id is not UNSET:
            field_dict["account_id"] = account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = d.pop("status")

        duration_ms = d.pop("duration_ms")

        job_id = d.pop("job_id", UNSET)

        run_id = d.pop("run_id", UNSET)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        job_finished_webhook_payload = cls(
            status=status,
            duration_ms=duration_ms,
            job_id=job_id,
            run_id=run_id,
            finished_at=finished_at,
            account_id=account_id,
        )

        job_finished_webhook_payload.additional_properties = d
        return job_finished_webhook_payload

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
