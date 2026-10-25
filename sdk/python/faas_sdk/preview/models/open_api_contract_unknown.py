from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OpenAPIContractUnknown")


@_attrs_define
class OpenAPIContractUnknown:
    """One changed unsupported response-schema feature or incomplete baseline that the current comparator cannot classify
    as breaking or additive.

    """

    path: str
    method: str
    """HTTP method for the affected operation."""
    code: str
    """Stable classification code for the unsupported schema change."""
    status: str | Unset = UNSET
    path_in_schema: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        method = self.method

        code = self.code

        status = self.status

        path_in_schema = self.path_in_schema

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "path": path,
                "method": method,
                "code": code,
            }
        )
        if status is not UNSET:
            field_dict["status"] = status
        if path_in_schema is not UNSET:
            field_dict["path_in_schema"] = path_in_schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        method = d.pop("method")

        code = d.pop("code")

        status = d.pop("status", UNSET)

        path_in_schema = d.pop("path_in_schema", UNSET)

        open_api_contract_unknown = cls(
            path=path,
            method=method,
            code=code,
            status=status,
            path_in_schema=path_in_schema,
        )

        open_api_contract_unknown.additional_properties = d
        return open_api_contract_unknown

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
