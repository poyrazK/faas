from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.runtime_upgrade_preview_response_changes_item import (
    RuntimeUpgradePreviewResponseChangesItem,
    check_runtime_upgrade_preview_response_changes_item,
)
from ..models.runtime_upgrade_preview_response_disposition import (
    RuntimeUpgradePreviewResponseDisposition,
    check_runtime_upgrade_preview_response_disposition,
)

if TYPE_CHECKING:
    from ..models.runtime_release_response import RuntimeReleaseResponse


T = TypeVar("T", bound="RuntimeUpgradePreviewResponse")


@_attrs_define
class RuntimeUpgradePreviewResponse:
    """Component comparison and qualification requirements for a read-only runtime update plan."""

    deployment_id: str
    current: None | RuntimeReleaseResponse
    target: RuntimeReleaseResponse
    """Exact published runtime base bytes; host kernel and function runner are separate components."""
    disposition: RuntimeUpgradePreviewResponseDisposition
    changes: list[RuntimeUpgradePreviewResponseChangesItem]
    blockers: list[str]
    required_steps: list[str]
    rebuild_required: bool
    cold_start_required: bool
    execution_available: bool
    """Preview only; no runtime update operation is implemented."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.runtime_release_response import RuntimeReleaseResponse

        deployment_id = self.deployment_id

        current: dict[str, Any] | None
        if isinstance(self.current, RuntimeReleaseResponse):
            current = self.current.to_dict()
        else:
            current = self.current

        target = self.target.to_dict()

        disposition: str = self.disposition

        changes = []
        for changes_item_data in self.changes:
            changes_item: str = changes_item_data
            changes.append(changes_item)

        blockers = self.blockers

        required_steps = self.required_steps

        rebuild_required = self.rebuild_required

        cold_start_required = self.cold_start_required

        execution_available = self.execution_available

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
                "current": current,
                "target": target,
                "disposition": disposition,
                "changes": changes,
                "blockers": blockers,
                "required_steps": required_steps,
                "rebuild_required": rebuild_required,
                "cold_start_required": cold_start_required,
                "execution_available": execution_available,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.runtime_release_response import RuntimeReleaseResponse

        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        def _parse_current(data: object) -> None | RuntimeReleaseResponse:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                current_type_0 = RuntimeReleaseResponse.from_dict(data)

                return current_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RuntimeReleaseResponse, data)

        current = _parse_current(d.pop("current"))

        target = RuntimeReleaseResponse.from_dict(d.pop("target"))

        disposition = check_runtime_upgrade_preview_response_disposition(d.pop("disposition"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = check_runtime_upgrade_preview_response_changes_item(changes_item_data)

            changes.append(changes_item)

        blockers = cast(list[str], d.pop("blockers"))

        required_steps = cast(list[str], d.pop("required_steps"))

        rebuild_required = d.pop("rebuild_required")

        cold_start_required = d.pop("cold_start_required")

        execution_available = d.pop("execution_available")

        runtime_upgrade_preview_response = cls(
            deployment_id=deployment_id,
            current=current,
            target=target,
            disposition=disposition,
            changes=changes,
            blockers=blockers,
            required_steps=required_steps,
            rebuild_required=rebuild_required,
            cold_start_required=cold_start_required,
            execution_available=execution_available,
        )

        return runtime_upgrade_preview_response
