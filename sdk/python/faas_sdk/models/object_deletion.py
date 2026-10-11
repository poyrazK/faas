from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_deletion_last_error_code import ObjectDeletionLastErrorCode, check_object_deletion_last_error_code
from ..models.object_deletion_state import ObjectDeletionState, check_object_deletion_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectDeletion")


@_attrs_define
class ObjectDeletion:
    """Durable deletion receipt; uncertain attempts retain their bucket fence."""

    id: UUID
    bucket_id: UUID
    key: str
    selector: str
    """Empty for ordinary deletion; null or the selected owned public version UUID otherwise."""
    state: ObjectDeletionState
    delete_marker: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    version_id: str | Unset = UNSET
    """Selected public version UUID or new public marker UUID or null when acknowledged; private provider IDs are
    never exposed."""
    last_error_code: ObjectDeletionLastErrorCode | Unset = UNSET
    """object_protected defers a lifecycle target under retention or a hold until a later scan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        bucket_id = str(self.bucket_id)

        key = self.key

        selector = self.selector

        state: str = self.state

        delete_marker = self.delete_marker

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        version_id = self.version_id

        last_error_code: str | Unset = UNSET
        if not isinstance(self.last_error_code, Unset):
            last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "bucket_id": bucket_id,
                "key": key,
                "selector": selector,
                "state": state,
                "delete_marker": delete_marker,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if version_id is not UNSET:
            field_dict["version_id"] = version_id
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        bucket_id = UUID(d.pop("bucket_id"))

        key = d.pop("key")

        selector = d.pop("selector")

        state = check_object_deletion_state(d.pop("state"))

        delete_marker = d.pop("delete_marker")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        version_id = d.pop("version_id", UNSET)

        _last_error_code = d.pop("last_error_code", UNSET)
        last_error_code: ObjectDeletionLastErrorCode | Unset
        if isinstance(_last_error_code, Unset):
            last_error_code = UNSET
        else:
            last_error_code = check_object_deletion_last_error_code(_last_error_code)

        object_deletion = cls(
            id=id,
            bucket_id=bucket_id,
            key=key,
            selector=selector,
            state=state,
            delete_marker=delete_marker,
            created_at=created_at,
            updated_at=updated_at,
            version_id=version_id,
            last_error_code=last_error_code,
        )

        object_deletion.additional_properties = d
        return object_deletion

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
