from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_connection_issue import DataConnectionIssue
    from ..models.data_source_column import DataSourceColumn


T = TypeVar("T", bound="DataSourceTable")


@_attrs_define
class DataSourceTable:
    """A discovered public table. Unsupported tables remain visible with reasons and cannot be selected for replication."""

    name: str
    supported: bool
    columns: list[DataSourceColumn]
    issues: list[DataConnectionIssue]
    column_selection_supported: bool | Unset = UNSET
    """A valid explicit column selection may omit incompatible non-key columns; all table-level checks still apply."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        supported = self.supported

        columns = []
        for columns_item_data in self.columns:
            columns_item = columns_item_data.to_dict()
            columns.append(columns_item)

        issues = []
        for issues_item_data in self.issues:
            issues_item = issues_item_data.to_dict()
            issues.append(issues_item)

        column_selection_supported = self.column_selection_supported

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "supported": supported,
                "columns": columns,
                "issues": issues,
            }
        )
        if column_selection_supported is not UNSET:
            field_dict["column_selection_supported"] = column_selection_supported

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_connection_issue import DataConnectionIssue
        from ..models.data_source_column import DataSourceColumn

        d = dict(src_dict)
        name = d.pop("name")

        supported = d.pop("supported")

        columns = []
        _columns = d.pop("columns")
        for columns_item_data in _columns:
            columns_item = DataSourceColumn.from_dict(columns_item_data)

            columns.append(columns_item)

        issues = []
        _issues = d.pop("issues")
        for issues_item_data in _issues:
            issues_item = DataConnectionIssue.from_dict(issues_item_data)

            issues.append(issues_item)

        column_selection_supported = d.pop("column_selection_supported", UNSET)

        data_source_table = cls(
            name=name,
            supported=supported,
            columns=columns,
            issues=issues,
            column_selection_supported=column_selection_supported,
        )

        return data_source_table
