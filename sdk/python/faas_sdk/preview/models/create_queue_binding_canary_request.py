from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateQueueBindingCanaryRequest")


@_attrs_define
class CreateQueueBindingCanaryRequest:
    """Publication consent and deployment selection for a bounded push queue canary."""

    deployment_id: UUID
    binding: str
    ttl_seconds: int
    publish_consent: bool

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        binding = self.binding

        ttl_seconds = self.ttl_seconds

        publish_consent = self.publish_consent

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
                "binding": binding,
                "ttl_seconds": ttl_seconds,
                "publish_consent": publish_consent,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        binding = d.pop("binding")

        ttl_seconds = d.pop("ttl_seconds")

        publish_consent = d.pop("publish_consent")

        create_queue_binding_canary_request = cls(
            deployment_id=deployment_id,
            binding=binding,
            ttl_seconds=ttl_seconds,
            publish_consent=publish_consent,
        )

        return create_queue_binding_canary_request
