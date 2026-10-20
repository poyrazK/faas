from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.app_response import AppResponse
    from ..models.queue_binding_response import QueueBindingResponse
    from ..models.scaling_policy import ScalingPolicy


T = TypeVar("T", bound="QueueWorkloadProfileResponse")


@_attrs_define
class QueueWorkloadProfileResponse:
    """The converged queue binding and queue-depth scaling policy."""

    app: AppResponse
    """An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy
    pointer, per-app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169
    / #172)."""
    binding: QueueBindingResponse
    """Durable queue-to-workload binding."""
    scaling_policy: ScalingPolicy
    """Per-app autoscaling configuration (issue #462 / ADR-058). Mirrors the on-disk jsonb column
    `apps.scaling_policy`. Empty values map to the engine default (the apid gate is load-bearing for the floor /
    ceiling, not the encoder). PR-A persists the DTO; PR-C wires the engine; PR-D carves out the worker-class
    branch."""
    created: bool
    """True when the default binding was created by this request."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app.to_dict()

        binding = self.binding.to_dict()

        scaling_policy = self.scaling_policy.to_dict()

        created = self.created

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "binding": binding,
                "scaling_policy": scaling_policy,
                "created": created,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_response import AppResponse
        from ..models.queue_binding_response import QueueBindingResponse
        from ..models.scaling_policy import ScalingPolicy

        d = dict(src_dict)
        app = AppResponse.from_dict(d.pop("app"))

        binding = QueueBindingResponse.from_dict(d.pop("binding"))

        scaling_policy = ScalingPolicy.from_dict(d.pop("scaling_policy"))

        created = d.pop("created")

        queue_workload_profile_response = cls(
            app=app,
            binding=binding,
            scaling_policy=scaling_policy,
            created=created,
        )

        queue_workload_profile_response.additional_properties = d
        return queue_workload_profile_response

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
