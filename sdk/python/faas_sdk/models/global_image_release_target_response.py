from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_image_release_target_response_diagnostic import (
    GlobalImageReleaseTargetResponseDiagnostic,
    check_global_image_release_target_response_diagnostic,
)
from ..models.global_image_release_target_response_outcome import (
    GlobalImageReleaseTargetResponseOutcome,
    check_global_image_release_target_response_outcome,
)

T = TypeVar("T", bound="GlobalImageReleaseTargetResponse")


@_attrs_define
class GlobalImageReleaseTargetResponse:
    """Immutable regional release target and durable sanitized outcome."""

    cell_id: UUID
    region: str
    cell_generation: int
    regional_account_id: UUID
    regional_app_id: UUID
    regional_app_slug: str
    outcome: GlobalImageReleaseTargetResponseOutcome
    diagnostic: GlobalImageReleaseTargetResponseDiagnostic
    deployment_id: None | UUID
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cell_id = str(self.cell_id)

        region = self.region

        cell_generation = self.cell_generation

        regional_account_id = str(self.regional_account_id)

        regional_app_id = str(self.regional_app_id)

        regional_app_slug = self.regional_app_slug

        outcome: str = self.outcome

        diagnostic: str = self.diagnostic

        deployment_id: None | str
        if isinstance(self.deployment_id, UUID):
            deployment_id = str(self.deployment_id)
        else:
            deployment_id = self.deployment_id

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cell_id": cell_id,
                "region": region,
                "cell_generation": cell_generation,
                "regional_account_id": regional_account_id,
                "regional_app_id": regional_app_id,
                "regional_app_slug": regional_app_slug,
                "outcome": outcome,
                "diagnostic": diagnostic,
                "deployment_id": deployment_id,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cell_id = UUID(d.pop("cell_id"))

        region = d.pop("region")

        cell_generation = d.pop("cell_generation")

        regional_account_id = UUID(d.pop("regional_account_id"))

        regional_app_id = UUID(d.pop("regional_app_id"))

        regional_app_slug = d.pop("regional_app_slug")

        outcome = check_global_image_release_target_response_outcome(d.pop("outcome"))

        diagnostic = check_global_image_release_target_response_diagnostic(d.pop("diagnostic"))

        def _parse_deployment_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deployment_id_type_0 = UUID(data)

                return deployment_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        deployment_id = _parse_deployment_id(d.pop("deployment_id"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        global_image_release_target_response = cls(
            cell_id=cell_id,
            region=region,
            cell_generation=cell_generation,
            regional_account_id=regional_account_id,
            regional_app_id=regional_app_id,
            regional_app_slug=regional_app_slug,
            outcome=outcome,
            diagnostic=diagnostic,
            deployment_id=deployment_id,
            updated_at=updated_at,
        )

        global_image_release_target_response.additional_properties = d
        return global_image_release_target_response

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
