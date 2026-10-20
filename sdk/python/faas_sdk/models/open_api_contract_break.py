from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.open_api_contract_break_kind import OpenAPIContractBreakKind, check_open_api_contract_break_kind
from ..models.open_api_contract_break_method import OpenAPIContractBreakMethod, check_open_api_contract_break_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="OpenAPIContractBreak")


@_attrs_define
class OpenAPIContractBreak:
    """One structural breaking change in an API response schema."""

    path: str
    method: OpenAPIContractBreakMethod
    kind: OpenAPIContractBreakKind
    status: str | Unset = UNSET
    path_in_schema: str | Unset = UNSET
    before: Any | Unset = UNSET
    after: Any | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        method: str = self.method

        kind: str = self.kind

        status = self.status

        path_in_schema = self.path_in_schema

        before = self.before

        after = self.after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "path": path,
                "method": method,
                "kind": kind,
            }
        )
        if status is not UNSET:
            field_dict["status"] = status
        if path_in_schema is not UNSET:
            field_dict["path_in_schema"] = path_in_schema
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        method = check_open_api_contract_break_method(d.pop("method"))

        kind = check_open_api_contract_break_kind(d.pop("kind"))

        status = d.pop("status", UNSET)

        path_in_schema = d.pop("path_in_schema", UNSET)

        before = d.pop("before", UNSET)

        after = d.pop("after", UNSET)

        open_api_contract_break = cls(
            path=path,
            method=method,
            kind=kind,
            status=status,
            path_in_schema=path_in_schema,
            before=before,
            after=after,
        )

        open_api_contract_break.additional_properties = d
        return open_api_contract_break

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
