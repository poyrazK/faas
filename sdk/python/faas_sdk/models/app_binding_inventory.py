from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_binding_inventory_item import AppBindingInventoryItem
    from ..models.binding_inventory_issue import BindingInventoryIssue
    from ..models.binding_runtime_freshness import BindingRuntimeFreshness


T = TypeVar("T", bound="AppBindingInventory")


@_attrs_define
class AppBindingInventory:
    """Best-effort app binding metadata with explicit completeness and sanitized section issues."""

    app: str
    generated_at: datetime.datetime
    complete: bool
    """All binding sections could be read; this does not mean that bindings are healthy or verified."""
    bindings: list[AppBindingInventoryItem]
    scope: str | Unset = UNSET
    """Resource scope filter. Absent means all scopes."""
    requested_deployment_id: UUID | Unset = UNSET
    """Confirms that the explicit deployment_id selector was applied. Absent for default selection."""
    verification_deployment_id: UUID | Unset = UNSET
    """Deployment selected for evidence: explicit selector or current manual-task deployment."""
    verification_scope: str | Unset = UNSET
    """Scope of the selected verification deployment."""
    runtime_freshness: BindingRuntimeFreshness | Unset = UNSET
    """App-wide timestamp-based configuration freshness, independent of canary verification. This is not guest
    acknowledgement, readiness or proof of credential use. Scope filtering selects deployments; task guests, jobs
    and mirrors are excluded."""
    issues: list[BindingInventoryIssue] | Unset = UNSET
    warnings: list[str] | Unset = UNSET
    """Human-readable, sanitized messages for each issue."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        generated_at = self.generated_at.isoformat()

        complete = self.complete

        bindings = []
        for bindings_item_data in self.bindings:
            bindings_item = bindings_item_data.to_dict()
            bindings.append(bindings_item)

        scope = self.scope

        requested_deployment_id: str | Unset = UNSET
        if not isinstance(self.requested_deployment_id, Unset):
            requested_deployment_id = str(self.requested_deployment_id)

        verification_deployment_id: str | Unset = UNSET
        if not isinstance(self.verification_deployment_id, Unset):
            verification_deployment_id = str(self.verification_deployment_id)

        verification_scope = self.verification_scope

        runtime_freshness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.runtime_freshness, Unset):
            runtime_freshness = self.runtime_freshness.to_dict()

        issues: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.issues, Unset):
            issues = []
            for issues_item_data in self.issues:
                issues_item = issues_item_data.to_dict()
                issues.append(issues_item)

        warnings: list[str] | Unset = UNSET
        if not isinstance(self.warnings, Unset):
            warnings = self.warnings

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "generated_at": generated_at,
                "complete": complete,
                "bindings": bindings,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if requested_deployment_id is not UNSET:
            field_dict["requested_deployment_id"] = requested_deployment_id
        if verification_deployment_id is not UNSET:
            field_dict["verification_deployment_id"] = verification_deployment_id
        if verification_scope is not UNSET:
            field_dict["verification_scope"] = verification_scope
        if runtime_freshness is not UNSET:
            field_dict["runtime_freshness"] = runtime_freshness
        if issues is not UNSET:
            field_dict["issues"] = issues
        if warnings is not UNSET:
            field_dict["warnings"] = warnings

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_binding_inventory_item import AppBindingInventoryItem
        from ..models.binding_inventory_issue import BindingInventoryIssue
        from ..models.binding_runtime_freshness import BindingRuntimeFreshness

        d = dict(src_dict)
        app = d.pop("app")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        complete = d.pop("complete")

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = AppBindingInventoryItem.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        scope = d.pop("scope", UNSET)

        _requested_deployment_id = d.pop("requested_deployment_id", UNSET)
        requested_deployment_id: UUID | Unset
        if isinstance(_requested_deployment_id, Unset):
            requested_deployment_id = UNSET
        else:
            requested_deployment_id = UUID(_requested_deployment_id)

        _verification_deployment_id = d.pop("verification_deployment_id", UNSET)
        verification_deployment_id: UUID | Unset
        if isinstance(_verification_deployment_id, Unset):
            verification_deployment_id = UNSET
        else:
            verification_deployment_id = UUID(_verification_deployment_id)

        verification_scope = d.pop("verification_scope", UNSET)

        _runtime_freshness = d.pop("runtime_freshness", UNSET)
        runtime_freshness: BindingRuntimeFreshness | Unset
        if isinstance(_runtime_freshness, Unset):
            runtime_freshness = UNSET
        else:
            runtime_freshness = BindingRuntimeFreshness.from_dict(_runtime_freshness)

        _issues = d.pop("issues", UNSET)
        issues: list[BindingInventoryIssue] | Unset = UNSET
        if _issues is not UNSET:
            issues = []
            for issues_item_data in _issues:
                issues_item = BindingInventoryIssue.from_dict(issues_item_data)

                issues.append(issues_item)

        warnings = cast(list[str], d.pop("warnings", UNSET))

        app_binding_inventory = cls(
            app=app,
            generated_at=generated_at,
            complete=complete,
            bindings=bindings,
            scope=scope,
            requested_deployment_id=requested_deployment_id,
            verification_deployment_id=verification_deployment_id,
            verification_scope=verification_scope,
            runtime_freshness=runtime_freshness,
            issues=issues,
            warnings=warnings,
        )

        app_binding_inventory.additional_properties = d
        return app_binding_inventory

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
