from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.application_standard_enrollment_materialized_fields_item import (
    ApplicationStandardEnrollmentMaterializedFieldsItem,
    check_application_standard_enrollment_materialized_fields_item,
)
from ..models.application_standard_enrollment_state import (
    ApplicationStandardEnrollmentState,
    check_application_standard_enrollment_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_adoption import ApplicationStandardAdoption
    from ..models.application_standard_effective import ApplicationStandardEffective
    from ..models.application_standard_settings import ApplicationStandardSettings


T = TypeVar("T", bound="ApplicationStandardEnrollment")


@_attrs_define
class ApplicationStandardEnrollment:
    """Saved desired intent and the last installed projection, separately from consumer observation."""

    app_id: UUID
    org_id: UUID
    local_settings: ApplicationStandardSettings
    """Logical control values; resource references contain UUIDs, never credentials."""
    additional_log_destinations: list[UUID]
    adoptions: list[ApplicationStandardAdoption]
    """Captured adoption pins; publication never moves them automatically."""
    materialized_fields: list[ApplicationStandardEnrollmentMaterializedFieldsItem]
    desired_revision: int
    persisted_revision: int
    observed_revision: int
    state: ApplicationStandardEnrollmentState
    updated_at: datetime.datetime
    project_id: UUID | Unset = UNSET
    installed_effective: ApplicationStandardEffective | Unset = UNSET
    """Resolved control values with their inheritance sources and any constraint violations."""
    installed_effective_hash: str | Unset = UNSET
    installed_exception_expires_at: datetime.datetime | Unset = UNSET
    """Deadline of a contributing exception in the last persisted projection; it can already be expired while
    replacement is pending. Does not establish consumer observation."""
    error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        org_id = str(self.org_id)

        local_settings = self.local_settings.to_dict()

        additional_log_destinations = []
        for additional_log_destinations_item_data in self.additional_log_destinations:
            additional_log_destinations_item = str(additional_log_destinations_item_data)
            additional_log_destinations.append(additional_log_destinations_item)

        adoptions = []
        for adoptions_item_data in self.adoptions:
            adoptions_item = adoptions_item_data.to_dict()
            adoptions.append(adoptions_item)

        materialized_fields = []
        for materialized_fields_item_data in self.materialized_fields:
            materialized_fields_item: str = materialized_fields_item_data
            materialized_fields.append(materialized_fields_item)

        desired_revision = self.desired_revision

        persisted_revision = self.persisted_revision

        observed_revision = self.observed_revision

        state: str = self.state

        updated_at = self.updated_at.isoformat()

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        installed_effective: dict[str, Any] | Unset = UNSET
        if not isinstance(self.installed_effective, Unset):
            installed_effective = self.installed_effective.to_dict()

        installed_effective_hash = self.installed_effective_hash

        installed_exception_expires_at: str | Unset = UNSET
        if not isinstance(self.installed_exception_expires_at, Unset):
            installed_exception_expires_at = self.installed_exception_expires_at.isoformat()

        error_code = self.error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "org_id": org_id,
                "local_settings": local_settings,
                "additional_log_destinations": additional_log_destinations,
                "adoptions": adoptions,
                "materialized_fields": materialized_fields,
                "desired_revision": desired_revision,
                "persisted_revision": persisted_revision,
                "observed_revision": observed_revision,
                "state": state,
                "updated_at": updated_at,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if installed_effective is not UNSET:
            field_dict["installed_effective"] = installed_effective
        if installed_effective_hash is not UNSET:
            field_dict["installed_effective_hash"] = installed_effective_hash
        if installed_exception_expires_at is not UNSET:
            field_dict["installed_exception_expires_at"] = installed_exception_expires_at
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_adoption import ApplicationStandardAdoption
        from ..models.application_standard_effective import ApplicationStandardEffective
        from ..models.application_standard_settings import ApplicationStandardSettings

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        org_id = UUID(d.pop("org_id"))

        local_settings = ApplicationStandardSettings.from_dict(d.pop("local_settings"))

        additional_log_destinations = []
        _additional_log_destinations = d.pop("additional_log_destinations")
        for additional_log_destinations_item_data in _additional_log_destinations:
            additional_log_destinations_item = UUID(additional_log_destinations_item_data)

            additional_log_destinations.append(additional_log_destinations_item)

        adoptions = []
        _adoptions = d.pop("adoptions")
        for adoptions_item_data in _adoptions:
            adoptions_item = ApplicationStandardAdoption.from_dict(adoptions_item_data)

            adoptions.append(adoptions_item)

        materialized_fields = []
        _materialized_fields = d.pop("materialized_fields")
        for materialized_fields_item_data in _materialized_fields:
            materialized_fields_item = check_application_standard_enrollment_materialized_fields_item(
                materialized_fields_item_data
            )

            materialized_fields.append(materialized_fields_item)

        desired_revision = d.pop("desired_revision")

        persisted_revision = d.pop("persisted_revision")

        observed_revision = d.pop("observed_revision")

        state = check_application_standard_enrollment_state(d.pop("state"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        _installed_effective = d.pop("installed_effective", UNSET)
        installed_effective: ApplicationStandardEffective | Unset
        if isinstance(_installed_effective, Unset):
            installed_effective = UNSET
        else:
            installed_effective = ApplicationStandardEffective.from_dict(_installed_effective)

        installed_effective_hash = d.pop("installed_effective_hash", UNSET)

        _installed_exception_expires_at = d.pop("installed_exception_expires_at", UNSET)
        installed_exception_expires_at: datetime.datetime | Unset
        if isinstance(_installed_exception_expires_at, Unset):
            installed_exception_expires_at = UNSET
        else:
            installed_exception_expires_at = datetime.datetime.fromisoformat(_installed_exception_expires_at)

        error_code = d.pop("error_code", UNSET)

        application_standard_enrollment = cls(
            app_id=app_id,
            org_id=org_id,
            local_settings=local_settings,
            additional_log_destinations=additional_log_destinations,
            adoptions=adoptions,
            materialized_fields=materialized_fields,
            desired_revision=desired_revision,
            persisted_revision=persisted_revision,
            observed_revision=observed_revision,
            state=state,
            updated_at=updated_at,
            project_id=project_id,
            installed_effective=installed_effective,
            installed_effective_hash=installed_effective_hash,
            installed_exception_expires_at=installed_exception_expires_at,
            error_code=error_code,
        )

        application_standard_enrollment.additional_properties = d
        return application_standard_enrollment

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
