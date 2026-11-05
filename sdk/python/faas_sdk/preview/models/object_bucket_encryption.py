from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_bucket_encryption_state import ObjectBucketEncryptionState, check_object_bucket_encryption_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_encryption import ObjectEncryption


T = TypeVar("T", bound="ObjectBucketEncryption")


@_attrs_define
class ObjectBucketEncryption:
    """Owned desired and verified policies. Empty encryption means no owned override; providers may retain baseline
    encryption. New implicit writes require state ready.

    """

    bucket_id: UUID
    state: ObjectBucketEncryptionState
    revision: int
    updated_at: datetime.datetime
    encryption: ObjectEncryption | Unset = UNSET
    """Owned encryption selection for object writes and upload policies. KMS requires an enrolled Gregale key
    reference; bucket keys apply only to aws:kms. Context is canonical base64 of a bounded JSON object with unique
    string entries."""
    desired_encryption: ObjectEncryption | Unset = UNSET
    """Owned encryption selection for object writes and upload policies. KMS requires an enrolled Gregale key
    reference; bucket keys apply only to aws:kms. Context is canonical base64 of a bounded JSON object with unique
    string entries."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        state: str = self.state

        revision = self.revision

        updated_at = self.updated_at.isoformat()

        encryption: dict[str, Any] | Unset = UNSET
        if not isinstance(self.encryption, Unset):
            encryption = self.encryption.to_dict()

        desired_encryption: dict[str, Any] | Unset = UNSET
        if not isinstance(self.desired_encryption, Unset):
            desired_encryption = self.desired_encryption.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bucket_id": bucket_id,
                "state": state,
                "revision": revision,
                "updated_at": updated_at,
            }
        )
        if encryption is not UNSET:
            field_dict["encryption"] = encryption
        if desired_encryption is not UNSET:
            field_dict["desired_encryption"] = desired_encryption

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_encryption import ObjectEncryption

        d = dict(src_dict)
        bucket_id = UUID(d.pop("bucket_id"))

        state = check_object_bucket_encryption_state(d.pop("state"))

        revision = d.pop("revision")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _encryption = d.pop("encryption", UNSET)
        encryption: ObjectEncryption | Unset
        if isinstance(_encryption, Unset):
            encryption = UNSET
        else:
            encryption = ObjectEncryption.from_dict(_encryption)

        _desired_encryption = d.pop("desired_encryption", UNSET)
        desired_encryption: ObjectEncryption | Unset
        if isinstance(_desired_encryption, Unset):
            desired_encryption = UNSET
        else:
            desired_encryption = ObjectEncryption.from_dict(_desired_encryption)

        object_bucket_encryption = cls(
            bucket_id=bucket_id,
            state=state,
            revision=revision,
            updated_at=updated_at,
            encryption=encryption,
            desired_encryption=desired_encryption,
        )

        object_bucket_encryption.additional_properties = d
        return object_bucket_encryption

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
