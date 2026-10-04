from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_image_release_response_status import (
    GlobalImageReleaseResponseStatus,
    check_global_image_release_response_status,
)

if TYPE_CHECKING:
    from ..models.global_image_release_target_response import GlobalImageReleaseTargetResponse


T = TypeVar("T", bound="GlobalImageReleaseResponse")


@_attrs_define
class GlobalImageReleaseResponse:
    """Durable image intent and regional acceptance outcomes. No runtime readiness or ingress claim."""

    global_app_id: UUID
    release_id: UUID
    generation: int
    desired_generation: int
    image: str
    status: GlobalImageReleaseResponseStatus
    created_at: datetime.datetime
    targets: list[GlobalImageReleaseTargetResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        global_app_id = str(self.global_app_id)

        release_id = str(self.release_id)

        generation = self.generation

        desired_generation = self.desired_generation

        image = self.image

        status: str = self.status

        created_at = self.created_at.isoformat()

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "global_app_id": global_app_id,
                "release_id": release_id,
                "generation": generation,
                "desired_generation": desired_generation,
                "image": image,
                "status": status,
                "created_at": created_at,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.global_image_release_target_response import GlobalImageReleaseTargetResponse

        d = dict(src_dict)
        global_app_id = UUID(d.pop("global_app_id"))

        release_id = UUID(d.pop("release_id"))

        generation = d.pop("generation")

        desired_generation = d.pop("desired_generation")

        image = d.pop("image")

        status = check_global_image_release_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = GlobalImageReleaseTargetResponse.from_dict(targets_item_data)

            targets.append(targets_item)

        global_image_release_response = cls(
            global_app_id=global_app_id,
            release_id=release_id,
            generation=generation,
            desired_generation=desired_generation,
            image=image,
            status=status,
            created_at=created_at,
            targets=targets,
        )

        global_image_release_response.additional_properties = d
        return global_image_release_response

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
