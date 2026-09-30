from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.environment_git_ops_run import EnvironmentGitOpsRun
    from ..models.environment_git_source import EnvironmentGitSource


T = TypeVar("T", bound="EnvironmentGitOpsStatusResponse")


@_attrs_define
class EnvironmentGitOpsStatusResponse:
    """Source authority and the twenty most recent durable reconciliation attempts."""

    source: EnvironmentGitSource
    """Durable environment authority with separate approved and fully applied revision pointers."""
    runs: list[EnvironmentGitOpsRun]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_dict()

        runs = []
        for runs_item_data in self.runs:
            runs_item = runs_item_data.to_dict()
            runs.append(runs_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "runs": runs,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_run import EnvironmentGitOpsRun
        from ..models.environment_git_source import EnvironmentGitSource

        d = dict(src_dict)
        source = EnvironmentGitSource.from_dict(d.pop("source"))

        runs = []
        _runs = d.pop("runs")
        for runs_item_data in _runs:
            runs_item = EnvironmentGitOpsRun.from_dict(runs_item_data)

            runs.append(runs_item)

        environment_git_ops_status_response = cls(
            source=source,
            runs=runs,
        )

        environment_git_ops_status_response.additional_properties = d
        return environment_git_ops_status_response

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
