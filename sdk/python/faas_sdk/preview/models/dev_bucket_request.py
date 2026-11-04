from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_bucket_request_permission import DevBucketRequestPermission, check_dev_bucket_request_permission
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_storage_compute_trigger import ObjectStorageComputeTrigger


T = TypeVar("T", bound="DevBucketRequest")


@_attrs_define
class DevBucketRequest:
    """Opt-in private bucket in the developer workspace, bound in the default secret scope. Bucket and binding names are
    unique within the batch.

    """

    name: str
    region: str | Unset = UNSET
    """Omit to reuse existing placement or the current creation default."""
    permission: DevBucketRequestPermission | Unset = "read_write"
    prefix: str | Unset = UNSET
    """Defaults to GREGALE_S3_ followed by the normalized bucket name."""
    triggers: list[ObjectStorageComputeTrigger] | Unset = UNSET
    """Opt-in ObjectCreated triggers targeting this developer app. Omission preserves managed intent; an empty
    array clears it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        region = self.region

        permission: str | Unset = UNSET
        if not isinstance(self.permission, Unset):
            permission = self.permission

        prefix = self.prefix

        triggers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.triggers, Unset):
            triggers = []
            for triggers_item_data in self.triggers:
                triggers_item = triggers_item_data.to_dict()
                triggers.append(triggers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if region is not UNSET:
            field_dict["region"] = region
        if permission is not UNSET:
            field_dict["permission"] = permission
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if triggers is not UNSET:
            field_dict["triggers"] = triggers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_storage_compute_trigger import ObjectStorageComputeTrigger

        d = dict(src_dict)
        name = d.pop("name")

        region = d.pop("region", UNSET)

        _permission = d.pop("permission", UNSET)
        permission: DevBucketRequestPermission | Unset
        if isinstance(_permission, Unset):
            permission = UNSET
        else:
            permission = check_dev_bucket_request_permission(_permission)

        prefix = d.pop("prefix", UNSET)

        _triggers = d.pop("triggers", UNSET)
        triggers: list[ObjectStorageComputeTrigger] | Unset = UNSET
        if _triggers is not UNSET:
            triggers = []
            for triggers_item_data in _triggers:
                triggers_item = ObjectStorageComputeTrigger.from_dict(triggers_item_data)

                triggers.append(triggers_item)

        dev_bucket_request = cls(
            name=name,
            region=region,
            permission=permission,
            prefix=prefix,
            triggers=triggers,
        )

        dev_bucket_request.additional_properties = d
        return dev_bucket_request

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
