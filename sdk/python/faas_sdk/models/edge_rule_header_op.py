from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_header_op_action import EdgeRuleHeaderOpAction, check_edge_rule_header_op_action
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleHeaderOp")


@_attrs_define
class EdgeRuleHeaderOp:
    """One header mutation. `action` ∈ {add,set,remove}."""

    name: str
    action: EdgeRuleHeaderOpAction
    value: str | Unset = UNSET
    template: bool | Unset = UNSET
    """ADR-967. Expand ${name} request values in `value` (host, path, method, query, client_ip, country, asn,
    request_id, header:<name>, query:<name>, cookie:<name>;
    $$ is a literal $). Control characters are dropped and each
    value is capped at 1 KiB. Not allowed with action=remove.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        action: str = self.action

        value = self.value

        template = self.template

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "action": action,
            }
        )
        if value is not UNSET:
            field_dict["value"] = value
        if template is not UNSET:
            field_dict["template"] = template

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        action = check_edge_rule_header_op_action(d.pop("action"))

        value = d.pop("value", UNSET)

        template = d.pop("template", UNSET)

        edge_rule_header_op = cls(
            name=name,
            action=action,
            value=value,
            template=template,
        )

        edge_rule_header_op.additional_properties = d
        return edge_rule_header_op

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
