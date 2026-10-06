from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_encryption import ObjectEncryption
    from ..models.object_write_protection import ObjectWriteProtection


T = TypeVar("T", bound="CreateObjectMultipartUploadRequest")


@_attrs_define
class CreateObjectMultipartUploadRequest:
    """Final object identity, total size and optional owned encryption frozen at initiation for a resumable upload."""

    key: str
    size_bytes: int
    content_type: str | Unset = UNSET
    protection: ObjectWriteProtection | Unset = UNSET
    """Fixed or enrolled event retention and an independent legal hold for a new object version. Omitted retention
    inherits the immutable admitted bucket default. Event hold ON requires one days or years duration and permits an
    optional minimum date. Event hold OFF on creation requires an explicit fixed date and no duration. Governance
    bypass is unsupported."""
    encryption: ObjectEncryption | Unset = UNSET
    """Owned encryption selection for object writes and upload policies. KMS requires an enrolled Gregale key
    reference; bucket keys apply only to aws:kms. Context is canonical base64 of a bounded JSON object with unique
    string entries."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        size_bytes = self.size_bytes

        content_type = self.content_type

        protection: dict[str, Any] | Unset = UNSET
        if not isinstance(self.protection, Unset):
            protection = self.protection.to_dict()

        encryption: dict[str, Any] | Unset = UNSET
        if not isinstance(self.encryption, Unset):
            encryption = self.encryption.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "size_bytes": size_bytes,
            }
        )
        if content_type is not UNSET:
            field_dict["content_type"] = content_type
        if protection is not UNSET:
            field_dict["protection"] = protection
        if encryption is not UNSET:
            field_dict["encryption"] = encryption

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_encryption import ObjectEncryption
        from ..models.object_write_protection import ObjectWriteProtection

        d = dict(src_dict)
        key = d.pop("key")

        size_bytes = d.pop("size_bytes")

        content_type = d.pop("content_type", UNSET)

        _protection = d.pop("protection", UNSET)
        protection: ObjectWriteProtection | Unset
        if isinstance(_protection, Unset):
            protection = UNSET
        else:
            protection = ObjectWriteProtection.from_dict(_protection)

        _encryption = d.pop("encryption", UNSET)
        encryption: ObjectEncryption | Unset
        if isinstance(_encryption, Unset):
            encryption = UNSET
        else:
            encryption = ObjectEncryption.from_dict(_encryption)

        create_object_multipart_upload_request = cls(
            key=key,
            size_bytes=size_bytes,
            content_type=content_type,
            protection=protection,
            encryption=encryption,
        )

        return create_object_multipart_upload_request
