from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_encryption_algorithm import ObjectEncryptionAlgorithm, check_object_encryption_algorithm
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectEncryption")


@_attrs_define
class ObjectEncryption:
    """PUT-only owned encryption selection. KMS requires an enrolled Gregale key reference; bucket keys apply only to
    aws:kms. Context is canonical base64 of a bounded JSON object with unique string entries.

    """

    algorithm: ObjectEncryptionAlgorithm
    key_id: str | Unset = UNSET
    """Owned arn:gregale:kms reference; required for KMS and forbidden for AES256."""
    bucket_key_enabled: bool | Unset = UNSET
    context: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        algorithm: str = self.algorithm

        key_id = self.key_id

        bucket_key_enabled = self.bucket_key_enabled

        context = self.context

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "algorithm": algorithm,
            }
        )
        if key_id is not UNSET:
            field_dict["key_id"] = key_id
        if bucket_key_enabled is not UNSET:
            field_dict["bucket_key_enabled"] = bucket_key_enabled
        if context is not UNSET:
            field_dict["context"] = context

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        algorithm = check_object_encryption_algorithm(d.pop("algorithm"))

        key_id = d.pop("key_id", UNSET)

        bucket_key_enabled = d.pop("bucket_key_enabled", UNSET)

        context = d.pop("context", UNSET)

        object_encryption = cls(
            algorithm=algorithm,
            key_id=key_id,
            bucket_key_enabled=bucket_key_enabled,
            context=context,
        )

        return object_encryption
