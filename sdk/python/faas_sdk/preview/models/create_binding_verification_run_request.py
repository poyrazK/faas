from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_binding_verification_run_request_service_targets import (
        CreateBindingVerificationRunRequestServiceTargets,
    )


T = TypeVar("T", bound="CreateBindingVerificationRunRequest")


@_attrs_define
class CreateBindingVerificationRunRequest:
    """Review digest must match current policy, configuration and deployment; caller-selected UUID permits create response
    recovery.

    """

    id: UUID
    deployment_id: UUID
    review_digest: str
    ttl_seconds: int
    object_storage_write: bool
    queue_publish: bool
    max_concurrency: int | Unset = 2
    """Requested app receipt admission budget, from one to eight concurrent resources; defaults to two."""
    service_targets: CreateBindingVerificationRunRequestServiceTargets | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        deployment_id = str(self.deployment_id)

        review_digest = self.review_digest

        ttl_seconds = self.ttl_seconds

        object_storage_write = self.object_storage_write

        queue_publish = self.queue_publish

        max_concurrency = self.max_concurrency

        service_targets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_targets, Unset):
            service_targets = self.service_targets.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "deployment_id": deployment_id,
                "review_digest": review_digest,
                "ttl_seconds": ttl_seconds,
                "object_storage_write": object_storage_write,
                "queue_publish": queue_publish,
            }
        )
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if service_targets is not UNSET:
            field_dict["service_targets"] = service_targets

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_binding_verification_run_request_service_targets import (
            CreateBindingVerificationRunRequestServiceTargets,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        deployment_id = UUID(d.pop("deployment_id"))

        review_digest = d.pop("review_digest")

        ttl_seconds = d.pop("ttl_seconds")

        object_storage_write = d.pop("object_storage_write")

        queue_publish = d.pop("queue_publish")

        max_concurrency = d.pop("max_concurrency", UNSET)

        _service_targets = d.pop("service_targets", UNSET)
        service_targets: CreateBindingVerificationRunRequestServiceTargets | Unset
        if isinstance(_service_targets, Unset):
            service_targets = UNSET
        else:
            service_targets = CreateBindingVerificationRunRequestServiceTargets.from_dict(_service_targets)

        create_binding_verification_run_request = cls(
            id=id,
            deployment_id=deployment_id,
            review_digest=review_digest,
            ttl_seconds=ttl_seconds,
            object_storage_write=object_storage_write,
            queue_publish=queue_publish,
            max_concurrency=max_concurrency,
            service_targets=service_targets,
        )

        create_binding_verification_run_request.additional_properties = d
        return create_binding_verification_run_request

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
