from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_sign_request_method import ObjectSignRequestMethod, check_object_sign_request_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_sign_request_metadata import ObjectSignRequestMetadata
    from ..models.object_sign_request_tags import ObjectSignRequestTags


T = TypeVar("T", bound="ObjectSignRequest")


@_attrs_define
class ObjectSignRequest:
    """Exact object operation to authorize for a short time."""

    method: ObjectSignRequestMethod
    key: str
    expires_in: int | Unset = 300
    size_bytes: int | Unset = UNSET
    """Required for PUT; forbidden for GET."""
    content_type: str | Unset = UNSET
    """PUT only."""
    cache_control: str | Unset = UNSET
    """PUT only."""
    content_disposition: str | Unset = UNSET
    """PUT only."""
    content_encoding: str | Unset = UNSET
    """PUT only."""
    content_language: str | Unset = UNSET
    """PUT only."""
    metadata: ObjectSignRequestMetadata | Unset = UNSET
    """PUT-only x-amz-meta-* values."""
    tags: ObjectSignRequestTags | Unset = UNSET
    """PUT-only S3 object tags."""

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        key = self.key

        expires_in = self.expires_in

        size_bytes = self.size_bytes

        content_type = self.content_type

        cache_control = self.cache_control

        content_disposition = self.content_disposition

        content_encoding = self.content_encoding

        content_language = self.content_language

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        tags: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tags, Unset):
            tags = self.tags.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "method": method,
                "key": key,
            }
        )
        if expires_in is not UNSET:
            field_dict["expires_in"] = expires_in
        if size_bytes is not UNSET:
            field_dict["size_bytes"] = size_bytes
        if content_type is not UNSET:
            field_dict["content_type"] = content_type
        if cache_control is not UNSET:
            field_dict["cache_control"] = cache_control
        if content_disposition is not UNSET:
            field_dict["content_disposition"] = content_disposition
        if content_encoding is not UNSET:
            field_dict["content_encoding"] = content_encoding
        if content_language is not UNSET:
            field_dict["content_language"] = content_language
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if tags is not UNSET:
            field_dict["tags"] = tags

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_sign_request_metadata import ObjectSignRequestMetadata
        from ..models.object_sign_request_tags import ObjectSignRequestTags

        d = dict(src_dict)
        method = check_object_sign_request_method(d.pop("method"))

        key = d.pop("key")

        expires_in = d.pop("expires_in", UNSET)

        size_bytes = d.pop("size_bytes", UNSET)

        content_type = d.pop("content_type", UNSET)

        cache_control = d.pop("cache_control", UNSET)

        content_disposition = d.pop("content_disposition", UNSET)

        content_encoding = d.pop("content_encoding", UNSET)

        content_language = d.pop("content_language", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: ObjectSignRequestMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ObjectSignRequestMetadata.from_dict(_metadata)

        _tags = d.pop("tags", UNSET)
        tags: ObjectSignRequestTags | Unset
        if isinstance(_tags, Unset):
            tags = UNSET
        else:
            tags = ObjectSignRequestTags.from_dict(_tags)

        object_sign_request = cls(
            method=method,
            key=key,
            expires_in=expires_in,
            size_bytes=size_bytes,
            content_type=content_type,
            cache_control=cache_control,
            content_disposition=content_disposition,
            content_encoding=content_encoding,
            content_language=content_language,
            metadata=metadata,
            tags=tags,
        )

        return object_sign_request
