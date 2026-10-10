from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_match_expr_op import EdgeRuleMatchExprOp, check_edge_rule_match_expr_op
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleMatchExpr")


@_attrs_define
class EdgeRuleMatchExpr:
    """ADR-962 match condition, ANDed with the rule's fixed selectors. A node
    is exactly one of all, any, not, or a field/op leaf. Fields: method,
    path, host, client_ip, country, asn (ADR-966: the client IP's
    autonomous system, e.g. 13335 or AS13335), ua_family (ADR-968: the
    User-Agent's family, one of browser, mobile, bot, tool, other),
    verified_bot (ADR-968: a known crawler confirmed by forward-confirmed
    reverse DNS of the client IP, one of googlebot, bingbot, applebot,
    yandexbot, baiduspider, yahoo, petalbot; absent otherwise),
    header:<name>, cookie:<name>, query:<name>. ua_family and
    verified_bot take eq, ne, in, not_in, exists, missing. Ops: eq, ne, in, not_in, prefix, suffix, contains,
    exists, missing, regex (RE2), cidr (client_ip only), in_list (ADR-963:
    list names an account list whose kind fits the field). Depth at most 4,
    at most 32 nodes, 64 values per leaf, values and regexes at most 256
    bytes. An untrusted client IP, or its unknown country or ASN, is absent.

    """

    all_: list[EdgeRuleMatchExpr] | Unset = UNSET
    any_: list[EdgeRuleMatchExpr] | Unset = UNSET
    not_: EdgeRuleMatchExpr | Unset = UNSET
    """ADR-962 match condition, ANDed with the rule's fixed selectors. A node
    is exactly one of all, any, not, or a field/op leaf. Fields: method,
    path, host, client_ip, country, asn (ADR-966: the client IP's
    autonomous system, e.g. 13335 or AS13335), ua_family (ADR-968: the
    User-Agent's family, one of browser, mobile, bot, tool, other),
    verified_bot (ADR-968: a known crawler confirmed by forward-confirmed
    reverse DNS of the client IP, one of googlebot, bingbot, applebot,
    yandexbot, baiduspider, yahoo, petalbot; absent otherwise),
    header:<name>, cookie:<name>, query:<name>. ua_family and
    verified_bot take eq, ne, in, not_in, exists, missing. Ops: eq, ne, in, not_in, prefix, suffix, contains,
    exists, missing, regex (RE2), cidr (client_ip only), in_list (ADR-963:
    list names an account list whose kind fits the field). Depth at most 4,
    at most 32 nodes, 64 values per leaf, values and regexes at most 256
    bytes. An untrusted client IP, or its unknown country or ASN, is absent.
    """
    field: str | Unset = UNSET
    op: EdgeRuleMatchExprOp | Unset = UNSET
    value: str | Unset = UNSET
    values: list[str] | Unset = UNSET
    list_: str | Unset = UNSET
    """Account list name for op in_list."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        all_: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.all_, Unset):
            all_ = []
            for all_item_data in self.all_:
                all_item = all_item_data.to_dict()
                all_.append(all_item)

        any_: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.any_, Unset):
            any_ = []
            for any_item_data in self.any_:
                any_item = any_item_data.to_dict()
                any_.append(any_item)

        not_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.not_, Unset):
            not_ = self.not_.to_dict()

        field = self.field

        op: str | Unset = UNSET
        if not isinstance(self.op, Unset):
            op = self.op

        value = self.value

        values: list[str] | Unset = UNSET
        if not isinstance(self.values, Unset):
            values = self.values

        list_ = self.list_

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if all_ is not UNSET:
            field_dict["all"] = all_
        if any_ is not UNSET:
            field_dict["any"] = any_
        if not_ is not UNSET:
            field_dict["not"] = not_
        if field is not UNSET:
            field_dict["field"] = field
        if op is not UNSET:
            field_dict["op"] = op
        if value is not UNSET:
            field_dict["value"] = value
        if values is not UNSET:
            field_dict["values"] = values
        if list_ is not UNSET:
            field_dict["list"] = list_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _all_ = d.pop("all", UNSET)
        all_: list[EdgeRuleMatchExpr] | Unset = UNSET
        if _all_ is not UNSET:
            all_ = []
            for all_item_data in _all_:
                all_item = EdgeRuleMatchExpr.from_dict(all_item_data)

                all_.append(all_item)

        _any_ = d.pop("any", UNSET)
        any_: list[EdgeRuleMatchExpr] | Unset = UNSET
        if _any_ is not UNSET:
            any_ = []
            for any_item_data in _any_:
                any_item = EdgeRuleMatchExpr.from_dict(any_item_data)

                any_.append(any_item)

        _not_ = d.pop("not", UNSET)
        not_: EdgeRuleMatchExpr | Unset
        if isinstance(_not_, Unset):
            not_ = UNSET
        else:
            not_ = EdgeRuleMatchExpr.from_dict(_not_)

        field = d.pop("field", UNSET)

        _op = d.pop("op", UNSET)
        op: EdgeRuleMatchExprOp | Unset
        if isinstance(_op, Unset):
            op = UNSET
        else:
            op = check_edge_rule_match_expr_op(_op)

        value = d.pop("value", UNSET)

        values = cast(list[str], d.pop("values", UNSET))

        list_ = d.pop("list", UNSET)

        edge_rule_match_expr = cls(
            all_=all_,
            any_=any_,
            not_=not_,
            field=field,
            op=op,
            value=value,
            values=values,
            list_=list_,
        )

        edge_rule_match_expr.additional_properties = d
        return edge_rule_match_expr

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
