from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_operation_preview_admission import AppOperationPreviewAdmission, check_app_operation_preview_admission
from ..models.app_operation_preview_authorization import (
    AppOperationPreviewAuthorization,
    check_app_operation_preview_authorization,
)
from ..models.app_operation_preview_operation import AppOperationPreviewOperation, check_app_operation_preview_operation
from ..models.app_operation_preview_schema_version import (
    AppOperationPreviewSchemaVersion,
    check_app_operation_preview_schema_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_operation_preview_checks import AppOperationPreviewChecks


T = TypeVar("T", bound="AppOperationPreview")


@_attrs_define
class AppOperationPreview:
    """Read-only operation authorization snapshot with separate admission limitations."""

    schema_version: AppOperationPreviewSchemaVersion
    app_id: str
    scope: str
    operation: AppOperationPreviewOperation
    authorization: AppOperationPreviewAuthorization
    admission: AppOperationPreviewAdmission
    required_scopes: list[str]
    checks: AppOperationPreviewChecks
    checked_at: datetime.datetime
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schema_version: int = self.schema_version

        app_id = self.app_id

        scope = self.scope

        operation: str = self.operation

        authorization: str = self.authorization

        admission: str = self.admission

        required_scopes = self.required_scopes

        checks = self.checks.to_dict()

        checked_at = self.checked_at.isoformat()

        project_id = self.project_id

        environment_id = self.environment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schema_version": schema_version,
                "app_id": app_id,
                "scope": scope,
                "operation": operation,
                "authorization": authorization,
                "admission": admission,
                "required_scopes": required_scopes,
                "checks": checks,
                "checked_at": checked_at,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_operation_preview_checks import AppOperationPreviewChecks

        d = dict(src_dict)
        schema_version = check_app_operation_preview_schema_version(d.pop("schema_version"))

        app_id = d.pop("app_id")

        scope = d.pop("scope")

        operation = check_app_operation_preview_operation(d.pop("operation"))

        authorization = check_app_operation_preview_authorization(d.pop("authorization"))

        admission = check_app_operation_preview_admission(d.pop("admission"))

        required_scopes = cast(list[str], d.pop("required_scopes"))

        checks = AppOperationPreviewChecks.from_dict(d.pop("checks"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        app_operation_preview = cls(
            schema_version=schema_version,
            app_id=app_id,
            scope=scope,
            operation=operation,
            authorization=authorization,
            admission=admission,
            required_scopes=required_scopes,
            checks=checks,
            checked_at=checked_at,
            project_id=project_id,
            environment_id=environment_id,
        )

        app_operation_preview.additional_properties = d
        return app_operation_preview

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
