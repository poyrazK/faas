from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_capacity_reconciliation_state import (
    ObjectCapacityReconciliationState,
    check_object_capacity_reconciliation_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectCapacityReconciliation")


@_attrs_define
class ObjectCapacityReconciliation:
    """Durable fenced capacity inventory and quota rebase. Failed or blocked jobs retain reservations; billing is
    unaffected.

    """

    id: UUID
    bucket_id: UUID
    state: ObjectCapacityReconciliationState
    before_bytes: int
    before_keys: int
    after_bytes: int
    after_keys: int
    reclaimed_bytes: int
    reclaimed_keys: int
    pending_writes: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    last_error_code: str | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        bucket_id = str(self.bucket_id)

        state: str = self.state

        before_bytes = self.before_bytes

        before_keys = self.before_keys

        after_bytes = self.after_bytes

        after_keys = self.after_keys

        reclaimed_bytes = self.reclaimed_bytes

        reclaimed_keys = self.reclaimed_keys

        pending_writes = self.pending_writes

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        last_error_code = self.last_error_code

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "bucket_id": bucket_id,
                "state": state,
                "before_bytes": before_bytes,
                "before_keys": before_keys,
                "after_bytes": after_bytes,
                "after_keys": after_keys,
                "reclaimed_bytes": reclaimed_bytes,
                "reclaimed_keys": reclaimed_keys,
                "pending_writes": pending_writes,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        bucket_id = UUID(d.pop("bucket_id"))

        state = check_object_capacity_reconciliation_state(d.pop("state"))

        before_bytes = d.pop("before_bytes")

        before_keys = d.pop("before_keys")

        after_bytes = d.pop("after_bytes")

        after_keys = d.pop("after_keys")

        reclaimed_bytes = d.pop("reclaimed_bytes")

        reclaimed_keys = d.pop("reclaimed_keys")

        pending_writes = d.pop("pending_writes")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        last_error_code = d.pop("last_error_code", UNSET)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        object_capacity_reconciliation = cls(
            id=id,
            bucket_id=bucket_id,
            state=state,
            before_bytes=before_bytes,
            before_keys=before_keys,
            after_bytes=after_bytes,
            after_keys=after_keys,
            reclaimed_bytes=reclaimed_bytes,
            reclaimed_keys=reclaimed_keys,
            pending_writes=pending_writes,
            created_at=created_at,
            updated_at=updated_at,
            last_error_code=last_error_code,
            finished_at=finished_at,
        )

        object_capacity_reconciliation.additional_properties = d
        return object_capacity_reconciliation

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
