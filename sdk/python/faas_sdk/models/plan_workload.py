from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_workload_action import PlanWorkloadAction, check_plan_workload_action
from ..models.plan_workload_class import PlanWorkloadClass, check_plan_workload_class
from ..models.plan_workload_tier import PlanWorkloadTier, check_plan_workload_tier
from ..models.preview_service_calls_policy import PreviewServiceCallsPolicy, check_preview_service_calls_policy
from ..models.service_binding_policy import ServiceBindingPolicy, check_service_binding_policy
from ..models.service_binding_transport import ServiceBindingTransport, check_service_binding_transport
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.compose_healthcheck import ComposeHealthcheck
    from ..models.plan_detected_by import PlanDetectedBy
    from ..models.plan_workload_depends_on_conditions import PlanWorkloadDependsOnConditions
    from ..models.service_caller_scopes import ServiceCallerScopes
    from ..models.service_reliability_policies import ServiceReliabilityPolicies


T = TypeVar("T", bound="PlanWorkload")


@_attrs_define
class PlanWorkload:
    """One discovered unit of work. Mirrors reposcan.Workload."""

    name: str
    root_dir: str
    """Effective build context inside the uploaded repository archive. Workspace manifests and sibling packages
    remain available outside this directory."""
    command: list[str]
    ports: list[int]
    platform_tenant_required: bool | Unset = UNSET
    """Requested customer identity policy from Compose x-gregale-platform-tenant-required or the request override.
    Omitted preserves existing app policy; new apps default to false."""
    dockerfile: str | Unset = UNSET
    image: str | Unset = UNSET
    """Normalized prebuilt OCI image for a workload without build. Tags are resolved and pinned by the image worker
    before materialization; stateful images remain managed requirements. Image deployments return a deployment_id
    without a build_id."""
    image_healthcheck: ComposeHealthcheck | Unset = UNSET
    """Partial Compose override for a prebuilt image HEALTHCHECK. Empty test and zero timing/retry values inherit
    image settings. NONE disables the check. Durations retain nanosecond precision; positive durations must be at
    least 1ms."""
    depends_on: list[str] | Unset = UNSET
    """Compose service dependencies. The apply path validates the graph, deploys in dependency order, and injects
    GREGALE_SERVICE_<NAME>_URL plus GREGALE_SERVICE_<NAME>_HTTPS_URL for workload dependencies."""
    depends_on_conditions: PlanWorkloadDependsOnConditions | Unset = UNSET
    """Explicit Compose dependency conditions. service_started retains admission ordering; service_healthy gates
    release on the captured same-project, same-environment dependency deployment."""
    service_binding_policy: ServiceBindingPolicy | Unset = UNSET
    """Caller-side authorization policy for internal service requests. `account` preserves same-account
    reachability; `declared` permits only targets present in the caller's service bindings."""
    service_reliability: ServiceReliabilityPolicies | Unset = UNSET
    """Map of declared target service names to caller-owned reliability policies. Only names in this app's service
    bindings may appear."""
    service_binding_transport: ServiceBindingTransport | Unset = UNSET
    """Scheme used by the canonical GREGALE_SERVICE_<NAME>_URL environment variable. `https` selects the private
    `.internal` alias; `http` preserves the legacy `.svc.gregale` endpoint."""
    preview_service_calls_policy: PreviewServiceCallsPolicy | Unset = UNSET
    """Production target policy for internal service calls from preview apps. `allow` preserves existing behavior;
    `deny` rejects preview callers before waking the target."""
    allowed_service_callers: list[str] | Unset = UNSET
    """Target-side service allowlist from Compose `x-gregale-allow-callers`. Omitted permits same-account callers;
    an empty array denies all."""
    allowed_service_call_scopes: ServiceCallerScopes | Unset = UNSET
    """Target-owned service authorization map from logical caller app name to allowed HTTP methods and path
    prefixes. When present, callers missing from the map are denied."""
    class_: PlanWorkloadClass | Unset = UNSET
    schedule: str | Unset = UNSET
    """cron expression when declared (CronJob, render, serverless)"""
    env_keys: list[str] | Unset = UNSET
    """KEYS only — never values; spec §11 forbids logging secrets"""
    source: str | Unset = UNSET
    """detector provenance, e.g. compose.yaml: api"""
    tier: PlanWorkloadTier | Unset = UNSET
    action: PlanWorkloadAction | Unset = UNSET
    """ADR-124 blast-radius projection. create = workload is new to the account; update = existing app matches
    (root_dir, name)."""
    existing_app_id: str | Unset = UNSET
    """ADR-124: app row ID the update targets. Empty iff action == create."""
    detected_by: PlanDetectedBy | Unset = UNSET
    """Structured detection trace for one workload (issue #742).
    `source` on PlanWorkload carries the same provenance as free
    text ("compose.yaml: api"); this is the machine-readable form
    so a client can branch on the detector without parsing it.

    Additive and optional: absent on any response the server did
    not populate, so existing consumers are unaffected.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        root_dir = self.root_dir

        command = self.command

        ports = self.ports

        platform_tenant_required = self.platform_tenant_required

        dockerfile = self.dockerfile

        image = self.image

        image_healthcheck: dict[str, Any] | Unset = UNSET
        if not isinstance(self.image_healthcheck, Unset):
            image_healthcheck = self.image_healthcheck.to_dict()

        depends_on: list[str] | Unset = UNSET
        if not isinstance(self.depends_on, Unset):
            depends_on = self.depends_on

        depends_on_conditions: dict[str, Any] | Unset = UNSET
        if not isinstance(self.depends_on_conditions, Unset):
            depends_on_conditions = self.depends_on_conditions.to_dict()

        service_binding_policy: str | Unset = UNSET
        if not isinstance(self.service_binding_policy, Unset):
            service_binding_policy = self.service_binding_policy

        service_reliability: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_reliability, Unset):
            service_reliability = self.service_reliability.to_dict()

        service_binding_transport: str | Unset = UNSET
        if not isinstance(self.service_binding_transport, Unset):
            service_binding_transport = self.service_binding_transport

        preview_service_calls_policy: str | Unset = UNSET
        if not isinstance(self.preview_service_calls_policy, Unset):
            preview_service_calls_policy = self.preview_service_calls_policy

        allowed_service_callers: list[str] | Unset = UNSET
        if not isinstance(self.allowed_service_callers, Unset):
            allowed_service_callers = self.allowed_service_callers

        allowed_service_call_scopes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.allowed_service_call_scopes, Unset):
            allowed_service_call_scopes = self.allowed_service_call_scopes.to_dict()

        class_: str | Unset = UNSET
        if not isinstance(self.class_, Unset):
            class_ = self.class_

        schedule = self.schedule

        env_keys: list[str] | Unset = UNSET
        if not isinstance(self.env_keys, Unset):
            env_keys = self.env_keys

        source = self.source

        tier: str | Unset = UNSET
        if not isinstance(self.tier, Unset):
            tier = self.tier

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        existing_app_id = self.existing_app_id

        detected_by: dict[str, Any] | Unset = UNSET
        if not isinstance(self.detected_by, Unset):
            detected_by = self.detected_by.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "root_dir": root_dir,
                "command": command,
                "ports": ports,
            }
        )
        if platform_tenant_required is not UNSET:
            field_dict["platform_tenant_required"] = platform_tenant_required
        if dockerfile is not UNSET:
            field_dict["dockerfile"] = dockerfile
        if image is not UNSET:
            field_dict["image"] = image
        if image_healthcheck is not UNSET:
            field_dict["image_healthcheck"] = image_healthcheck
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on
        if depends_on_conditions is not UNSET:
            field_dict["depends_on_conditions"] = depends_on_conditions
        if service_binding_policy is not UNSET:
            field_dict["service_binding_policy"] = service_binding_policy
        if service_reliability is not UNSET:
            field_dict["service_reliability"] = service_reliability
        if service_binding_transport is not UNSET:
            field_dict["service_binding_transport"] = service_binding_transport
        if preview_service_calls_policy is not UNSET:
            field_dict["preview_service_calls_policy"] = preview_service_calls_policy
        if allowed_service_callers is not UNSET:
            field_dict["allowed_service_callers"] = allowed_service_callers
        if allowed_service_call_scopes is not UNSET:
            field_dict["allowed_service_call_scopes"] = allowed_service_call_scopes
        if class_ is not UNSET:
            field_dict["class"] = class_
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if env_keys is not UNSET:
            field_dict["env_keys"] = env_keys
        if source is not UNSET:
            field_dict["source"] = source
        if tier is not UNSET:
            field_dict["tier"] = tier
        if action is not UNSET:
            field_dict["action"] = action
        if existing_app_id is not UNSET:
            field_dict["existing_app_id"] = existing_app_id
        if detected_by is not UNSET:
            field_dict["detected_by"] = detected_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.compose_healthcheck import ComposeHealthcheck
        from ..models.plan_detected_by import PlanDetectedBy
        from ..models.plan_workload_depends_on_conditions import PlanWorkloadDependsOnConditions
        from ..models.service_caller_scopes import ServiceCallerScopes
        from ..models.service_reliability_policies import ServiceReliabilityPolicies

        d = dict(src_dict)
        name = d.pop("name")

        root_dir = d.pop("root_dir")

        command = cast(list[str], d.pop("command"))

        ports = cast(list[int], d.pop("ports"))

        platform_tenant_required = d.pop("platform_tenant_required", UNSET)

        dockerfile = d.pop("dockerfile", UNSET)

        image = d.pop("image", UNSET)

        _image_healthcheck = d.pop("image_healthcheck", UNSET)
        image_healthcheck: ComposeHealthcheck | Unset
        if isinstance(_image_healthcheck, Unset):
            image_healthcheck = UNSET
        else:
            image_healthcheck = ComposeHealthcheck.from_dict(_image_healthcheck)

        depends_on = cast(list[str], d.pop("depends_on", UNSET))

        _depends_on_conditions = d.pop("depends_on_conditions", UNSET)
        depends_on_conditions: PlanWorkloadDependsOnConditions | Unset
        if isinstance(_depends_on_conditions, Unset):
            depends_on_conditions = UNSET
        else:
            depends_on_conditions = PlanWorkloadDependsOnConditions.from_dict(_depends_on_conditions)

        _service_binding_policy = d.pop("service_binding_policy", UNSET)
        service_binding_policy: ServiceBindingPolicy | Unset
        if isinstance(_service_binding_policy, Unset):
            service_binding_policy = UNSET
        else:
            service_binding_policy = check_service_binding_policy(_service_binding_policy)

        _service_reliability = d.pop("service_reliability", UNSET)
        service_reliability: ServiceReliabilityPolicies | Unset
        if isinstance(_service_reliability, Unset):
            service_reliability = UNSET
        else:
            service_reliability = ServiceReliabilityPolicies.from_dict(_service_reliability)

        _service_binding_transport = d.pop("service_binding_transport", UNSET)
        service_binding_transport: ServiceBindingTransport | Unset
        if isinstance(_service_binding_transport, Unset):
            service_binding_transport = UNSET
        else:
            service_binding_transport = check_service_binding_transport(_service_binding_transport)

        _preview_service_calls_policy = d.pop("preview_service_calls_policy", UNSET)
        preview_service_calls_policy: PreviewServiceCallsPolicy | Unset
        if isinstance(_preview_service_calls_policy, Unset):
            preview_service_calls_policy = UNSET
        else:
            preview_service_calls_policy = check_preview_service_calls_policy(_preview_service_calls_policy)

        allowed_service_callers = cast(list[str], d.pop("allowed_service_callers", UNSET))

        _allowed_service_call_scopes = d.pop("allowed_service_call_scopes", UNSET)
        allowed_service_call_scopes: ServiceCallerScopes | Unset
        if isinstance(_allowed_service_call_scopes, Unset):
            allowed_service_call_scopes = UNSET
        else:
            allowed_service_call_scopes = ServiceCallerScopes.from_dict(_allowed_service_call_scopes)

        _class_ = d.pop("class", UNSET)
        class_: PlanWorkloadClass | Unset
        if isinstance(_class_, Unset):
            class_ = UNSET
        else:
            class_ = check_plan_workload_class(_class_)

        schedule = d.pop("schedule", UNSET)

        env_keys = cast(list[str], d.pop("env_keys", UNSET))

        source = d.pop("source", UNSET)

        _tier = d.pop("tier", UNSET)
        tier: PlanWorkloadTier | Unset
        if isinstance(_tier, Unset):
            tier = UNSET
        else:
            tier = check_plan_workload_tier(_tier)

        _action = d.pop("action", UNSET)
        action: PlanWorkloadAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_plan_workload_action(_action)

        existing_app_id = d.pop("existing_app_id", UNSET)

        _detected_by = d.pop("detected_by", UNSET)
        detected_by: PlanDetectedBy | Unset
        if isinstance(_detected_by, Unset):
            detected_by = UNSET
        else:
            detected_by = PlanDetectedBy.from_dict(_detected_by)

        plan_workload = cls(
            name=name,
            root_dir=root_dir,
            command=command,
            ports=ports,
            platform_tenant_required=platform_tenant_required,
            dockerfile=dockerfile,
            image=image,
            image_healthcheck=image_healthcheck,
            depends_on=depends_on,
            depends_on_conditions=depends_on_conditions,
            service_binding_policy=service_binding_policy,
            service_reliability=service_reliability,
            service_binding_transport=service_binding_transport,
            preview_service_calls_policy=preview_service_calls_policy,
            allowed_service_callers=allowed_service_callers,
            allowed_service_call_scopes=allowed_service_call_scopes,
            class_=class_,
            schedule=schedule,
            env_keys=env_keys,
            source=source,
            tier=tier,
            action=action,
            existing_app_id=existing_app_id,
            detected_by=detected_by,
        )

        plan_workload.additional_properties = d
        return plan_workload

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
