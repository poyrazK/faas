from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

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

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "supported": supported,
                "columns": columns,
                "issues": issues,
            }
        )

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

        data_source_table = cls(
            name=name,
            supported=supported,
            columns=columns,
            issues=issues,
        )

        return data_source_table
