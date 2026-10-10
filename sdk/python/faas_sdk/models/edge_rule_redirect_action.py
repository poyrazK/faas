from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_redirect_action_status_code import (
    EdgeRuleRedirectActionStatusCode,
    check_edge_rule_redirect_action_status_code,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_redirect_action_headers import EdgeRuleRedirectActionHeaders


T = TypeVar("T", bound="EdgeRuleRedirectAction")


@_attrs_define
class EdgeRuleRedirectAction:
    """3xx short-circuit."""

    status_code: EdgeRuleRedirectActionStatusCode
    to: str
    headers: EdgeRuleRedirectActionHeaders | Unset = UNSET
    """Headers stamped on the redirect response."""
    template: bool | Unset = UNSET
    """ADR-967. Expand ${name} request values in `to` and in the
    header values (host, path, method, query, client_ip, country, asn, request_id, header:<name>, query:<name>,
    cookie:<name>; $$ is a
    literal $). The target must start with a literal "/" or
    "http(s)://", and only ${host} may appear before the path.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status_code: int = self.status_code

        to = self.to

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        template = self.template

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status_code": status_code,
                "to": to,
            }
        )
        if headers is not UNSET:
            field_dict["headers"] = headers
        if template is not UNSET:
            field_dict["template"] = template

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_redirect_action_headers import EdgeRuleRedirectActionHeaders

        d = dict(src_dict)
        status_code = check_edge_rule_redirect_action_status_code(d.pop("status_code"))

        to = d.pop("to")

        _headers = d.pop("headers", UNSET)
        headers: EdgeRuleRedirectActionHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = EdgeRuleRedirectActionHeaders.from_dict(_headers)

        template = d.pop("template", UNSET)

        edge_rule_redirect_action = cls(
            status_code=status_code,
            to=to,
            headers=headers,
            template=template,
        )

        edge_rule_redirect_action.additional_properties = d
        return edge_rule_redirect_action

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
