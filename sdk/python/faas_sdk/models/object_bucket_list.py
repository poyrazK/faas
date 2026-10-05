from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_bucket_list_upload_profile import (
    ObjectBucketListUploadProfile,
    check_object_bucket_list_upload_profile,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_bucket import ObjectBucket


T = TypeVar("T", bound="ObjectBucketList")


@_attrs_define
class ObjectBucketList:
    """App bucket catalog and operator-configured preview capabilities."""

    items: list[ObjectBucket]
    enabled: bool
    regions: list[str]
    default_region: str
    max_upload_bytes: int
    max_buckets_per_app: int
    max_single_put_bytes: int | Unset = UNSET
    """Largest single PUT accepted by the configured upload profile."""
    max_part_bytes: int | Unset = UNSET
    """Largest multipart part accepted by the configured upload profile."""
    transfer_timeout_seconds: int | Unset = UNSET
    """Combined request staging and forwarding deadline in seconds."""
    upload_profile: ObjectBucketListUploadProfile | Unset = UNSET
    """Configured body size contract for the branded storage origin."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        enabled = self.enabled

        regions = self.regions

        default_region = self.default_region

        max_upload_bytes = self.max_upload_bytes

        max_buckets_per_app = self.max_buckets_per_app

        max_single_put_bytes = self.max_single_put_bytes

        max_part_bytes = self.max_part_bytes

        transfer_timeout_seconds = self.transfer_timeout_seconds

        upload_profile: str | Unset = UNSET
        if not isinstance(self.upload_profile, Unset):
            upload_profile = self.upload_profile

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "enabled": enabled,
                "regions": regions,
                "default_region": default_region,
                "max_upload_bytes": max_upload_bytes,
                "max_buckets_per_app": max_buckets_per_app,
            }
        )
        if max_single_put_bytes is not UNSET:
            field_dict["max_single_put_bytes"] = max_single_put_bytes
        if max_part_bytes is not UNSET:
            field_dict["max_part_bytes"] = max_part_bytes
        if transfer_timeout_seconds is not UNSET:
            field_dict["transfer_timeout_seconds"] = transfer_timeout_seconds
        if upload_profile is not UNSET:
            field_dict["upload_profile"] = upload_profile

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_bucket import ObjectBucket

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObjectBucket.from_dict(items_item_data)

            items.append(items_item)

        enabled = d.pop("enabled")

        regions = cast(list[str], d.pop("regions"))

        default_region = d.pop("default_region")

        max_upload_bytes = d.pop("max_upload_bytes")

        max_buckets_per_app = d.pop("max_buckets_per_app")

        max_single_put_bytes = d.pop("max_single_put_bytes", UNSET)

        max_part_bytes = d.pop("max_part_bytes", UNSET)

        transfer_timeout_seconds = d.pop("transfer_timeout_seconds", UNSET)

        _upload_profile = d.pop("upload_profile", UNSET)
        upload_profile: ObjectBucketListUploadProfile | Unset
        if isinstance(_upload_profile, Unset):
            upload_profile = UNSET
        else:
            upload_profile = check_object_bucket_list_upload_profile(_upload_profile)

        object_bucket_list = cls(
            items=items,
            enabled=enabled,
            regions=regions,
            default_region=default_region,
            max_upload_bytes=max_upload_bytes,
            max_buckets_per_app=max_buckets_per_app,
            max_single_put_bytes=max_single_put_bytes,
            max_part_bytes=max_part_bytes,
            transfer_timeout_seconds=transfer_timeout_seconds,
            upload_profile=upload_profile,
        )

        object_bucket_list.additional_properties = d
        return object_bucket_list

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
