from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.application_standard_settings import ApplicationStandardSettings


T = TypeVar("T", bound="SetApplicationStandardLocalIntentRequest")


@_attrs_define
class SetApplicationStandardLocalIntentRequest:
    expected_revision: int
    settings: ApplicationStandardSettings
    """Logical control values; resource references contain UUIDs, never credentials."""
    additional_log_destinations: list[UUID]

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        settings = self.settings.to_dict()

        additional_log_destinations = []
        for additional_log_destinations_item_data in self.additional_log_destinations:
            additional_log_destinations_item = str(additional_log_destinations_item_data)
            additional_log_destinations.append(additional_log_destinations_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "settings": settings,
                "additional_log_destinations": additional_log_destinations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_settings import ApplicationStandardSettings

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        settings = ApplicationStandardSettings.from_dict(d.pop("settings"))

        additional_log_destinations = []
        _additional_log_destinations = d.pop("additional_log_destinations")
        for additional_log_destinations_item_data in _additional_log_destinations:
            additional_log_destinations_item = UUID(additional_log_destinations_item_data)

            additional_log_destinations.append(additional_log_destinations_item)

        set_application_standard_local_intent_request = cls(
            expected_revision=expected_revision,
            settings=settings,
            additional_log_destinations=additional_log_destinations,
        )

        return set_application_standard_local_intent_request
