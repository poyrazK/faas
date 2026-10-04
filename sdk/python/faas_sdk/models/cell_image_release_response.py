from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CellImageReleaseResponse")


@_attrs_define
class CellImageReleaseResponse:
    """Durable regional acceptance receipt. This does not establish global readiness or routing eligibility."""

    cell_id: UUID
    region: str
    regional_account_id: UUID
    regional_app_id: UUID
    release_id: UUID
    image: str
    deployment_id: None | UUID
    """Null after collection; the release cannot be executed again."""
    deployment_status: str
    """Current regional deployment status, or expired after collection."""
    created_at: datetime.datetime
    replayed: bool
    """Whether PUT returned an existing acceptance receipt. Always false for GET."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cell_id = str(self.cell_id)

        region = self.region

        regional_account_id = str(self.regional_account_id)

        regional_app_id = str(self.regional_app_id)

        release_id = str(self.release_id)

        image = self.image

        deployment_id: None | str
        if isinstance(self.deployment_id, UUID):
            deployment_id = str(self.deployment_id)
        else:
            deployment_id = self.deployment_id

        deployment_status = self.deployment_status

        created_at = self.created_at.isoformat()

        replayed = self.replayed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cell_id": cell_id,
                "region": region,
                "regional_account_id": regional_account_id,
                "regional_app_id": regional_app_id,
                "release_id": release_id,
                "image": image,
                "deployment_id": deployment_id,
                "deployment_status": deployment_status,
                "created_at": created_at,
                "replayed": replayed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cell_id = UUID(d.pop("cell_id"))

        region = d.pop("region")

        regional_account_id = UUID(d.pop("regional_account_id"))

        regional_app_id = UUID(d.pop("regional_app_id"))

        release_id = UUID(d.pop("release_id"))

        image = d.pop("image")

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

        deployment_status = d.pop("deployment_status")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        replayed = d.pop("replayed")

        cell_image_release_response = cls(
            cell_id=cell_id,
            region=region,
            regional_account_id=regional_account_id,
            regional_app_id=regional_app_id,
            release_id=release_id,
            image=image,
            deployment_id=deployment_id,
            deployment_status=deployment_status,
            created_at=created_at,
            replayed=replayed,
        )

        cell_image_release_response.additional_properties = d
        return cell_image_release_response

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
