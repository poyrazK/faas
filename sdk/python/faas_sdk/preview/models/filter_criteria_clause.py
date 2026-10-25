from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.filter_criteria_op import FilterCriteriaOp, check_filter_criteria_op
from ..types import UNSET, Unset

T = TypeVar("T", bound="FilterCriteriaClause")


@_attrs_define
class FilterCriteriaClause:
    """One filter clause. The top-level FilterCriteria carries
    `$or`, `$and`, and `payload` arrays; each clause here
    is one primitive (eq / neq / exists / jsonpath) or a
    nested `$or` / `$and` for compound logic.

    """

    op: FilterCriteriaOp
    """Filter operation primitive (ADR-118 §6)."""
    field: str | Unset = UNSET
    """Header key for eq / neq / exists on the headers map."""
    value: Any | Unset = UNSET
    """Equality target for eq / neq; jsonpath expected type for jsonpath."""
    path: str | Unset = UNSET
    """Jsonpath expression for op=jsonpath. Evaluated against the record payload."""
    clauses: list[FilterCriteriaClause] | Unset = UNSET
    """Nested compound clauses for op=$or / $and."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        op: str = self.op

        field = self.field

        value = self.value

        path = self.path

        clauses: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.clauses, Unset):
            clauses = []
            for clauses_item_data in self.clauses:
                clauses_item = clauses_item_data.to_dict()
                clauses.append(clauses_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "op": op,
            }
        )
        if field is not UNSET:
            field_dict["field"] = field
        if value is not UNSET:
            field_dict["value"] = value
        if path is not UNSET:
            field_dict["path"] = path
        if clauses is not UNSET:
            field_dict["clauses"] = clauses

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        op = check_filter_criteria_op(d.pop("op"))

        field = d.pop("field", UNSET)

        value = d.pop("value", UNSET)

        path = d.pop("path", UNSET)

        _clauses = d.pop("clauses", UNSET)
        clauses: list[FilterCriteriaClause] | Unset = UNSET
        if _clauses is not UNSET:
            clauses = []
            for clauses_item_data in _clauses:
                clauses_item = FilterCriteriaClause.from_dict(clauses_item_data)

                clauses.append(clauses_item)

        filter_criteria_clause = cls(
            op=op,
            field=field,
            value=value,
            path=path,
            clauses=clauses,
        )

        filter_criteria_clause.additional_properties = d
        return filter_criteria_clause

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
