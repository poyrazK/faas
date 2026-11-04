from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_header_op import EdgeRuleHeaderOp


T = TypeVar("T", bound="EdgeRuleHeadersAction")


@_attrs_define
class EdgeRuleHeadersAction:
    """Mutates request + response headers. The gateway enforces a hard-coded blacklist (Host, Content-Length, Transfer-
    Encoding, Connection, x-faas-*).

    """

    request_headers: list[EdgeRuleHeaderOp] | Unset = UNSET
    response_headers: list[EdgeRuleHeaderOp] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_headers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.request_headers, Unset):
            request_headers = []
            for request_headers_item_data in self.request_headers:
                request_headers_item = request_headers_item_data.to_dict()
                request_headers.append(request_headers_item)

        response_headers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.response_headers, Unset):
            response_headers = []
            for response_headers_item_data in self.response_headers:
                response_headers_item = response_headers_item_data.to_dict()
                response_headers.append(response_headers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if request_headers is not UNSET:
            field_dict["request_headers"] = request_headers
        if response_headers is not UNSET:
            field_dict["response_headers"] = response_headers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_header_op import EdgeRuleHeaderOp

        d = dict(src_dict)
        _request_headers = d.pop("request_headers", UNSET)
        request_headers: list[EdgeRuleHeaderOp] | Unset = UNSET
        if _request_headers is not UNSET:
            request_headers = []
            for request_headers_item_data in _request_headers:
                request_headers_item = EdgeRuleHeaderOp.from_dict(request_headers_item_data)

                request_headers.append(request_headers_item)

        _response_headers = d.pop("response_headers", UNSET)
        response_headers: list[EdgeRuleHeaderOp] | Unset = UNSET
        if _response_headers is not UNSET:
            response_headers = []
            for response_headers_item_data in _response_headers:
                response_headers_item = EdgeRuleHeaderOp.from_dict(response_headers_item_data)

                response_headers.append(response_headers_item)

        edge_rule_headers_action = cls(
            request_headers=request_headers,
            response_headers=response_headers,
        )

        edge_rule_headers_action.additional_properties = d
        return edge_rule_headers_action

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
