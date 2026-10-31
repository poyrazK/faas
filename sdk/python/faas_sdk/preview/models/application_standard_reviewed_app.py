from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_reviewed_app_changed_fields_item import (
    ApplicationStandardReviewedAppChangedFieldsItem,
    check_application_standard_reviewed_app_changed_fields_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_adoption import ApplicationStandardAdoption
    from ..models.application_standard_effective import ApplicationStandardEffective
    from ..models.application_standard_settings import ApplicationStandardSettings


T = TypeVar("T", bound="ApplicationStandardReviewedApp")


@_attrs_define
class ApplicationStandardReviewedApp:
    """Application inputs, adoption pins and resolved changes captured in a saved review."""

    app_id: UUID
    slug: str
    desired_revision: int
    before_settings: ApplicationStandardSettings
    """Logical control values; resource references contain UUIDs, never credentials."""
    before_adoptions: list[ApplicationStandardAdoption]
    after_adoptions: list[ApplicationStandardAdoption]
    local_settings: ApplicationStandardSettings
    """Logical control values; resource references contain UUIDs, never credentials."""
    additional_log_destinations: list[UUID]
    effective: ApplicationStandardEffective
    """Resolved control values with their inheritance sources and any constraint violations."""
    changed_fields: list[ApplicationStandardReviewedAppChangedFieldsItem]
    project_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        slug = self.slug

        desired_revision = self.desired_revision

        before_settings = self.before_settings.to_dict()

        before_adoptions = []
        for before_adoptions_item_data in self.before_adoptions:
            before_adoptions_item = before_adoptions_item_data.to_dict()
            before_adoptions.append(before_adoptions_item)

        after_adoptions = []
        for after_adoptions_item_data in self.after_adoptions:
            after_adoptions_item = after_adoptions_item_data.to_dict()
            after_adoptions.append(after_adoptions_item)

        local_settings = self.local_settings.to_dict()

        additional_log_destinations = []
        for additional_log_destinations_item_data in self.additional_log_destinations:
            additional_log_destinations_item = str(additional_log_destinations_item_data)
            additional_log_destinations.append(additional_log_destinations_item)

        effective = self.effective.to_dict()

        changed_fields = []
        for changed_fields_item_data in self.changed_fields:
            changed_fields_item: str = changed_fields_item_data
            changed_fields.append(changed_fields_item)

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "desired_revision": desired_revision,
                "before_settings": before_settings,
                "before_adoptions": before_adoptions,
                "after_adoptions": after_adoptions,
                "local_settings": local_settings,
                "additional_log_destinations": additional_log_destinations,
                "effective": effective,
                "changed_fields": changed_fields,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_adoption import ApplicationStandardAdoption
        from ..models.application_standard_effective import ApplicationStandardEffective
        from ..models.application_standard_settings import ApplicationStandardSettings

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        slug = d.pop("slug")

        desired_revision = d.pop("desired_revision")

        before_settings = ApplicationStandardSettings.from_dict(d.pop("before_settings"))

        before_adoptions = []
        _before_adoptions = d.pop("before_adoptions")
        for before_adoptions_item_data in _before_adoptions:
            before_adoptions_item = ApplicationStandardAdoption.from_dict(before_adoptions_item_data)

            before_adoptions.append(before_adoptions_item)

        after_adoptions = []
        _after_adoptions = d.pop("after_adoptions")
        for after_adoptions_item_data in _after_adoptions:
            after_adoptions_item = ApplicationStandardAdoption.from_dict(after_adoptions_item_data)

            after_adoptions.append(after_adoptions_item)

        local_settings = ApplicationStandardSettings.from_dict(d.pop("local_settings"))

        additional_log_destinations = []
        _additional_log_destinations = d.pop("additional_log_destinations")
        for additional_log_destinations_item_data in _additional_log_destinations:
            additional_log_destinations_item = UUID(additional_log_destinations_item_data)

            additional_log_destinations.append(additional_log_destinations_item)

        effective = ApplicationStandardEffective.from_dict(d.pop("effective"))

        changed_fields = []
        _changed_fields = d.pop("changed_fields")
        for changed_fields_item_data in _changed_fields:
            changed_fields_item = check_application_standard_reviewed_app_changed_fields_item(changed_fields_item_data)

            changed_fields.append(changed_fields_item)

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        application_standard_reviewed_app = cls(
            app_id=app_id,
            slug=slug,
            desired_revision=desired_revision,
            before_settings=before_settings,
            before_adoptions=before_adoptions,
            after_adoptions=after_adoptions,
            local_settings=local_settings,
            additional_log_destinations=additional_log_destinations,
            effective=effective,
            changed_fields=changed_fields,
            project_id=project_id,
        )

        return application_standard_reviewed_app
