from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.filter_criteria_clause import FilterCriteriaClause


T = TypeVar("T", bound="FilterCriteria")


@_attrs_define
class FilterCriteria:
    """FilterCriteria on a trigger (migration 00300,
    pkg/sched/filter.go). nil / omitted matches every record.
    Top-level arrays combine via implicit OR for `$or` and
    AND for `$and`; nested clauses honour the same shape.
    Jsonpath implementation: github.com/PaesslerAG/jsonpath —
    no eval semantics, no customer-supplied code execution.

    """

    or_: list[FilterCriteriaClause] | Unset = UNSET
    and_: list[FilterCriteriaClause] | Unset = UNSET
    payload: list[FilterCriteriaClause] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        or_: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.or_, Unset):
            or_ = []
            for or_item_data in self.or_:
                or_item = or_item_data.to_dict()
                or_.append(or_item)

        and_: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.and_, Unset):
            and_ = []
            for and_item_data in self.and_:
                and_item = and_item_data.to_dict()
                and_.append(and_item)

        payload: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = []
            for payload_item_data in self.payload:
                payload_item = payload_item_data.to_dict()
                payload.append(payload_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if or_ is not UNSET:
            field_dict["$or"] = or_
        if and_ is not UNSET:
            field_dict["$and"] = and_
        if payload is not UNSET:
            field_dict["payload"] = payload

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.filter_criteria_clause import FilterCriteriaClause

        d = dict(src_dict)
        _or_ = d.pop("$or", UNSET)
        or_: list[FilterCriteriaClause] | Unset = UNSET
        if _or_ is not UNSET:
            or_ = []
            for or_item_data in _or_:
                or_item = FilterCriteriaClause.from_dict(or_item_data)

                or_.append(or_item)

        _and_ = d.pop("$and", UNSET)
        and_: list[FilterCriteriaClause] | Unset = UNSET
        if _and_ is not UNSET:
            and_ = []
            for and_item_data in _and_:
                and_item = FilterCriteriaClause.from_dict(and_item_data)

                and_.append(and_item)

        _payload = d.pop("payload", UNSET)
        payload: list[FilterCriteriaClause] | Unset = UNSET
        if _payload is not UNSET:
            payload = []
            for payload_item_data in _payload:
                payload_item = FilterCriteriaClause.from_dict(payload_item_data)

                payload.append(payload_item)

        filter_criteria = cls(
            or_=or_,
            and_=and_,
            payload=payload,
        )

        filter_criteria.additional_properties = d
        return filter_criteria

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
