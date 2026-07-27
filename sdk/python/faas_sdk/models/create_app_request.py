from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_app_request_runtime import CreateAppRequestRuntime, check_create_app_request_runtime
from ..models.create_app_request_type import CreateAppRequestType, check_create_app_request_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppRequest")


@_attrs_define
class CreateAppRequest:
    """App creation payload: slug, type (app|function), runtime (only for function), RAM MB, max concurrency, idle timeout,
    and optional manifest.

    """

    slug: str
    type_: CreateAppRequestType | Unset = UNSET
    runtime: CreateAppRequestRuntime | Unset = UNSET
    ram_mb: int | Unset = UNSET
    max_concurrency: int | Unset = UNSET
    idle_timeout_s: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        type_: str | Unset = UNSET
        if not isinstance(self.type_, Unset):
            type_ = self.type_

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        ram_mb = self.ram_mb

        max_concurrency = self.max_concurrency

        idle_timeout_s = self.idle_timeout_s

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
            }
        )
        if type_ is not UNSET:
            field_dict["type"] = type_
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if idle_timeout_s is not UNSET:
            field_dict["idle_timeout_s"] = idle_timeout_s

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        _type_ = d.pop("type", UNSET)
        type_: CreateAppRequestType | Unset
        if isinstance(_type_, Unset):
            type_ = UNSET
        else:
            type_ = check_create_app_request_type(_type_)

        _runtime = d.pop("runtime", UNSET)
        runtime: CreateAppRequestRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_create_app_request_runtime(_runtime)

        ram_mb = d.pop("ram_mb", UNSET)

        max_concurrency = d.pop("max_concurrency", UNSET)

        idle_timeout_s = d.pop("idle_timeout_s", UNSET)

        create_app_request = cls(
            slug=slug,
            type_=type_,
            runtime=runtime,
            ram_mb=ram_mb,
            max_concurrency=max_concurrency,
            idle_timeout_s=idle_timeout_s,
        )

        create_app_request.additional_properties = d
        return create_app_request

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
