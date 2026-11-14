from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_api_status_runtime_reports import DataAPIStatusRuntimeReports, check_data_api_status_runtime_reports
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_api_change_hook_status import DataAPIChangeHookStatus


T = TypeVar("T", bound="DataAPIStatus")


@_attrs_define
class DataAPIStatus:
    """What the platform knows about a Data API without waking it."""

    runtime_version: str
    """The runtime version this control plane serves; running Data APIs report theirs as runtime_version on
    /healthz."""
    runtime_reports: DataAPIStatusRuntimeReports
    """unavailable when this installation accepts no runtime reports: usage reports, monthly caps, keyless live
    reads and change hooks then do not reach the platform."""
    change_hooks: DataAPIChangeHookStatus | Unset = UNSET
    """The change hook delivery the app's runtime last reported."""

    def to_dict(self) -> dict[str, Any]:
        runtime_version = self.runtime_version

        runtime_reports: str = self.runtime_reports

        change_hooks: dict[str, Any] | Unset = UNSET
        if not isinstance(self.change_hooks, Unset):
            change_hooks = self.change_hooks.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "runtime_version": runtime_version,
                "runtime_reports": runtime_reports,
            }
        )
        if change_hooks is not UNSET:
            field_dict["change_hooks"] = change_hooks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_change_hook_status import DataAPIChangeHookStatus

        d = dict(src_dict)
        runtime_version = d.pop("runtime_version")

        runtime_reports = check_data_api_status_runtime_reports(d.pop("runtime_reports"))

        _change_hooks = d.pop("change_hooks", UNSET)
        change_hooks: DataAPIChangeHookStatus | Unset
        if isinstance(_change_hooks, Unset):
            change_hooks = UNSET
        else:
            change_hooks = DataAPIChangeHookStatus.from_dict(_change_hooks)

        data_api_status = cls(
            runtime_version=runtime_version,
            runtime_reports=runtime_reports,
            change_hooks=change_hooks,
        )

        return data_api_status
