from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.exclusive_operation_record_state import (
    ExclusiveOperationRecordState,
    check_exclusive_operation_record_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExclusiveOperationRecord")


@_attrs_define
class ExclusiveOperationRecord:
    """Public receipt. Accepted request contents, claim tokens, and renewal credentials are never returned."""

    id: UUID
    sequence: int
    state: ExclusiveOperationRecordState
    policy_revision: int
    generation: int
    """Monotonically increasing fencing generation for this coordination key."""
    created_at: datetime.datetime
    app_id: UUID | Unset = UNSET
    job_id: UUID | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    lease_expires_at: datetime.datetime | Unset = UNSET
    attempt_deadline: datetime.datetime | Unset = UNSET
    result: Any | Unset = UNSET
    """Platform-committed invocation result; arbitrary JSON value."""
    last_error: str | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        sequence = self.sequence

        state: str = self.state

        policy_revision = self.policy_revision

        generation = self.generation

        created_at = self.created_at.isoformat()

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        job_id: str | Unset = UNSET
        if not isinstance(self.job_id, Unset):
            job_id = str(self.job_id)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        lease_expires_at: str | Unset = UNSET
        if not isinstance(self.lease_expires_at, Unset):
            lease_expires_at = self.lease_expires_at.isoformat()

        attempt_deadline: str | Unset = UNSET
        if not isinstance(self.attempt_deadline, Unset):
            attempt_deadline = self.attempt_deadline.isoformat()

        result = self.result

        last_error = self.last_error

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "sequence": sequence,
                "state": state,
                "policy_revision": policy_revision,
                "generation": generation,
                "created_at": created_at,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if lease_expires_at is not UNSET:
            field_dict["lease_expires_at"] = lease_expires_at
        if attempt_deadline is not UNSET:
            field_dict["attempt_deadline"] = attempt_deadline
        if result is not UNSET:
            field_dict["result"] = result
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        sequence = d.pop("sequence")

        state = check_exclusive_operation_record_state(d.pop("state"))

        policy_revision = d.pop("policy_revision")

        generation = d.pop("generation")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _job_id = d.pop("job_id", UNSET)
        job_id: UUID | Unset
        if isinstance(_job_id, Unset):
            job_id = UNSET
        else:
            job_id = UUID(_job_id)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _lease_expires_at = d.pop("lease_expires_at", UNSET)
        lease_expires_at: datetime.datetime | Unset
        if isinstance(_lease_expires_at, Unset):
            lease_expires_at = UNSET
        else:
            lease_expires_at = datetime.datetime.fromisoformat(_lease_expires_at)

        _attempt_deadline = d.pop("attempt_deadline", UNSET)
        attempt_deadline: datetime.datetime | Unset
        if isinstance(_attempt_deadline, Unset):
            attempt_deadline = UNSET
        else:
            attempt_deadline = datetime.datetime.fromisoformat(_attempt_deadline)

        result = d.pop("result", UNSET)

        last_error = d.pop("last_error", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        exclusive_operation_record = cls(
            id=id,
            sequence=sequence,
            state=state,
            policy_revision=policy_revision,
            generation=generation,
            created_at=created_at,
            app_id=app_id,
            job_id=job_id,
            platform_tenant_id=platform_tenant_id,
            lease_expires_at=lease_expires_at,
            attempt_deadline=attempt_deadline,
            result=result,
            last_error=last_error,
            completed_at=completed_at,
        )

        exclusive_operation_record.additional_properties = d
        return exclusive_operation_record

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
