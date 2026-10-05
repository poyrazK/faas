from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_check_binding_result import BindingCheckBindingResult
    from ..models.binding_check_finding import BindingCheckFinding
    from ..models.binding_inventory_issue import BindingInventoryIssue
    from ..models.binding_runtime_deployment import BindingRuntimeDeployment


T = TypeVar("T", bound="BindingCheckReport")


@_attrs_define
class BindingCheckReport:
    """Safe preflight findings for the declared policy at checked_at. Coverage may be complete, partial or none; a passed
    report does not independently establish application readiness or credential use. Optional application
    acknowledgements are version-bound self-attestations.

    """

    app: str
    scope: str
    deployment_id: UUID
    checked_at: datetime.datetime
    inventory_generated_at: datetime.datetime
    max_verification_age: str
    allow_unsupported: bool
    passed: bool
    coverage: str
    bindings: list[BindingCheckBindingResult]
    runtime: list[BindingRuntimeDeployment]
    issues: list[BindingInventoryIssue]
    blockers: list[BindingCheckFinding]
    warnings: list[BindingCheckFinding]
    expected_deployment_id: UUID | Unset = UNSET
    require_application_ack: bool | Unset = UNSET
    """Whether this check required application self-attestations for current managed PostgreSQL/object-storage
    secrets."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        scope = self.scope

        deployment_id = str(self.deployment_id)

        checked_at = self.checked_at.isoformat()

        inventory_generated_at = self.inventory_generated_at.isoformat()

        max_verification_age = self.max_verification_age

        allow_unsupported = self.allow_unsupported

        passed = self.passed

        coverage = self.coverage

        bindings = []
        for bindings_item_data in self.bindings:
            bindings_item = bindings_item_data.to_dict()
            bindings.append(bindings_item)

        runtime = []
        for runtime_item_data in self.runtime:
            runtime_item = runtime_item_data.to_dict()
            runtime.append(runtime_item)

        issues = []
        for issues_item_data in self.issues:
            issues_item = issues_item_data.to_dict()
            issues.append(issues_item)

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        warnings = []
        for warnings_item_data in self.warnings:
            warnings_item = warnings_item_data.to_dict()
            warnings.append(warnings_item)

        expected_deployment_id: str | Unset = UNSET
        if not isinstance(self.expected_deployment_id, Unset):
            expected_deployment_id = str(self.expected_deployment_id)

        require_application_ack = self.require_application_ack

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "scope": scope,
                "deployment_id": deployment_id,
                "checked_at": checked_at,
                "inventory_generated_at": inventory_generated_at,
                "max_verification_age": max_verification_age,
                "allow_unsupported": allow_unsupported,
                "passed": passed,
                "coverage": coverage,
                "bindings": bindings,
                "runtime": runtime,
                "issues": issues,
                "blockers": blockers,
                "warnings": warnings,
            }
        )
        if expected_deployment_id is not UNSET:
            field_dict["expected_deployment_id"] = expected_deployment_id
        if require_application_ack is not UNSET:
            field_dict["require_application_ack"] = require_application_ack

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_binding_result import BindingCheckBindingResult
        from ..models.binding_check_finding import BindingCheckFinding
        from ..models.binding_inventory_issue import BindingInventoryIssue
        from ..models.binding_runtime_deployment import BindingRuntimeDeployment

        d = dict(src_dict)
        app = d.pop("app")

        scope = d.pop("scope")

        deployment_id = UUID(d.pop("deployment_id"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        inventory_generated_at = datetime.datetime.fromisoformat(d.pop("inventory_generated_at"))

        max_verification_age = d.pop("max_verification_age")

        allow_unsupported = d.pop("allow_unsupported")

        passed = d.pop("passed")

        coverage = d.pop("coverage")

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = BindingCheckBindingResult.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        runtime = []
        _runtime = d.pop("runtime")
        for runtime_item_data in _runtime:
            runtime_item = BindingRuntimeDeployment.from_dict(runtime_item_data)

            runtime.append(runtime_item)

        issues = []
        _issues = d.pop("issues")
        for issues_item_data in _issues:
            issues_item = BindingInventoryIssue.from_dict(issues_item_data)

            issues.append(issues_item)

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        warnings = []
        _warnings = d.pop("warnings")
        for warnings_item_data in _warnings:
            warnings_item = BindingCheckFinding.from_dict(warnings_item_data)

            warnings.append(warnings_item)

        _expected_deployment_id = d.pop("expected_deployment_id", UNSET)
        expected_deployment_id: UUID | Unset
        if isinstance(_expected_deployment_id, Unset):
            expected_deployment_id = UNSET
        else:
            expected_deployment_id = UUID(_expected_deployment_id)

        require_application_ack = d.pop("require_application_ack", UNSET)

        binding_check_report = cls(
            app=app,
            scope=scope,
            deployment_id=deployment_id,
            checked_at=checked_at,
            inventory_generated_at=inventory_generated_at,
            max_verification_age=max_verification_age,
            allow_unsupported=allow_unsupported,
            passed=passed,
            coverage=coverage,
            bindings=bindings,
            runtime=runtime,
            issues=issues,
            blockers=blockers,
            warnings=warnings,
            expected_deployment_id=expected_deployment_id,
            require_application_ack=require_application_ack,
        )

        binding_check_report.additional_properties = d
        return binding_check_report

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
