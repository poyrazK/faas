from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.data_pipeline_quality_rule_kind import DataPipelineQualityRuleKind, check_data_pipeline_quality_rule_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineQualityRule")


@_attrs_define
class DataPipelineQualityRule:
    """Structured business-data assertion on selected columns. Numeric bounds are exact decimal strings; reference rules
    match a complete selected target primary key and preserve tenant equality. SQL expressions are not accepted.

    """

    name: str
    kind: DataPipelineQualityRuleKind
    table: str
    columns: list[str]
    minimum: str | Unset = UNSET
    maximum: str | Unset = UNSET
    reference_table: str | Unset = UNSET
    reference_columns: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        kind: str = self.kind

        table = self.table

        columns = self.columns

        minimum = self.minimum

        maximum = self.maximum

        reference_table = self.reference_table

        reference_columns: list[str] | Unset = UNSET
        if not isinstance(self.reference_columns, Unset):
            reference_columns = self.reference_columns

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "kind": kind,
                "table": table,
                "columns": columns,
            }
        )
        if minimum is not UNSET:
            field_dict["minimum"] = minimum
        if maximum is not UNSET:
            field_dict["maximum"] = maximum
        if reference_table is not UNSET:
            field_dict["reference_table"] = reference_table
        if reference_columns is not UNSET:
            field_dict["reference_columns"] = reference_columns

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        kind = check_data_pipeline_quality_rule_kind(d.pop("kind"))

        table = d.pop("table")

        columns = cast(list[str], d.pop("columns"))

        minimum = d.pop("minimum", UNSET)

        maximum = d.pop("maximum", UNSET)

        reference_table = d.pop("reference_table", UNSET)

        reference_columns = cast(list[str], d.pop("reference_columns", UNSET))

        data_pipeline_quality_rule = cls(
            name=name,
            kind=kind,
            table=table,
            columns=columns,
            minimum=minimum,
            maximum=maximum,
            reference_table=reference_table,
            reference_columns=reference_columns,
        )

        return data_pipeline_quality_rule
