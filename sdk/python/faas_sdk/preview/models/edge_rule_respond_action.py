from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleRespondAction")


@_attrs_define
class EdgeRuleRespondAction:
    """Preview-only fixed JSON response. The gateway serves this body
    directly for the matched route, so the preview frontend can be
    developed before the backend endpoint exists. Status codes are
    limited to 200..599 and the JSON body is capped at 64 KiB.

    """

    status_code: int
    body: Any | Unset = UNSET
    """Arbitrary JSON response body. Omit for an empty response."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status_code = self.status_code

        body = self.body

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status_code": status_code,
            }
        )
        if body is not UNSET:
            field_dict["body"] = body

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status_code = d.pop("status_code")

        body = d.pop("body", UNSET)

        edge_rule_respond_action = cls(
            status_code=status_code,
            body=body,
        )

        edge_rule_respond_action.additional_properties = d
        return edge_rule_respond_action

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
