from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workload_uses_buckets_item_access import (
    WorkloadUsesBucketsItemAccess,
    check_workload_uses_buckets_item_access,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkloadUsesBucketsItem")


@_attrs_define
class WorkloadUsesBucketsItem:
    bucket: str
    app: str | Unset = UNSET
    """ Owning app (default self). """
    prefix: str | Unset = UNSET
    """ Key prefix the grant is limited to. """
    access: WorkloadUsesBucketsItemAccess | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bucket = self.bucket

        app = self.app

        prefix = self.prefix

        access: str | Unset = UNSET
        if not isinstance(self.access, Unset):
            access = self.access

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bucket": bucket,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if access is not UNSET:
            field_dict["access"] = access

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bucket = d.pop("bucket")

        app = d.pop("app", UNSET)

        prefix = d.pop("prefix", UNSET)

        _access = d.pop("access", UNSET)
        access: WorkloadUsesBucketsItemAccess | Unset
        if isinstance(_access, Unset):
            access = UNSET
        else:
            access = check_workload_uses_buckets_item_access(_access)

        workload_uses_buckets_item = cls(
            bucket=bucket,
            app=app,
            prefix=prefix,
            access=access,
        )

        workload_uses_buckets_item.additional_properties = d
        return workload_uses_buckets_item

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
