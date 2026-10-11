from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_manifest import AppManifest
    from ..models.environment_job_schedule import EnvironmentJobSchedule
    from ..models.environment_job_smoke import EnvironmentJobSmoke
    from ..models.environment_policy import EnvironmentPolicy
    from ..models.environment_route_contract import EnvironmentRouteContract
    from ..models.environment_workload_queue_bindings import EnvironmentWorkloadQueueBindings
    from ..models.environment_workload_queue_recoveries import EnvironmentWorkloadQueueRecoveries
    from ..models.environment_workload_queue_smoke import EnvironmentWorkloadQueueSmoke
    from ..models.environment_workload_secret_refs import EnvironmentWorkloadSecretRefs
    from ..models.environment_workload_service_bindings import EnvironmentWorkloadServiceBindings
    from ..models.environment_workload_source import EnvironmentWorkloadSource
    from ..models.environment_workload_variables import EnvironmentWorkloadVariables


T = TypeVar("T", bound="EnvironmentWorkload")


@_attrs_define
class EnvironmentWorkload:
    """Logical workload intent; unsupported adapters are reported as blocking reasons."""

    app: str | Unset = UNSET
    source: EnvironmentWorkloadSource | Unset = UNSET
    """Build source within the approved Git tree, a function runner, or an immutable OCI digest. Function sources
    require runtime and exclude dockerfile. The gated internal executor reserves new private workloads when app is
    omitted. Preparation does not grant serving authority."""
    runtime: AppManifest | Unset = UNSET
    """App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-
    as-source flag (§ux 6.3). The optional `env_secrets` field carries sealed-secret refs ("secret:NAME" strings)
    resolved by the host at wake time against the app_secrets table (issue #460 / ADR-053 §Decision 1). Values are
    NEVER sealed ciphertext — only refs. M-1 (ADR-136) widens the contract additively with `healthcheck`,
    `stop_signal`, `stop_grace_period` from the OCI image-config spec; old guest-init ignores unknown fields per
    JSON semantics, so the widen is wire-compatible. M-2 (ADR-137 + ADR-138) widens additively with
    `execution_mode`, `restart_policy`, `startup_deadline_s`, `max_retries`, and `service_replicas` — these govern
    the lifecycle contract (request vs service vs worker vs job) and the per-mode replica scaffold. Defaults
    preserve today's behaviour (execution_mode=request, restart_policy=on-failure)."""
    variables: EnvironmentWorkloadVariables | Unset = UNSET
    secret_refs: EnvironmentWorkloadSecretRefs | Unset = UNSET
    routes: EnvironmentRouteContract | Unset = UNSET
    """Atomic environment-scoped declared-route collection."""
    policies: list[EnvironmentPolicy] | Unset = UNSET
    queue_bindings: EnvironmentWorkloadQueueBindings | Unset = UNSET
    queue_smoke: EnvironmentWorkloadQueueSmoke | Unset = UNSET
    """Reviewed synthetic JSON input for each enabled push worker queue binding. Qualification sends it directly to
    the private candidate VM and never enqueues a customer message."""
    job_smoke: EnvironmentJobSmoke | Unset = UNSET
    """Reviewed argv-only command and short timeout for a job qualification attempt. The contract is frozen with
    the candidate; isolated execution and exit evidence are not yet available."""
    schedule: EnvironmentJobSchedule | Unset = UNSET
    """Reviewed recurring schedule for a job workload. Cron and timezone map to Gregale's durable Job schedule;
    production dispatch remains gated until the managed Job adapter is available."""
    queue_recoveries: EnvironmentWorkloadQueueRecoveries | Unset = UNSET
    """Explicit recovery of retained queues, keyed by a declared binding name and pinned to its original scoped
    binding UUID. Requires reviewed adoption before reconciliation can resume retained work; adoption itself
    preserves the retirement hold."""
    service_bindings: EnvironmentWorkloadServiceBindings | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.to_dict()

        runtime: dict[str, Any] | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime.to_dict()

        variables: dict[str, Any] | Unset = UNSET
        if not isinstance(self.variables, Unset):
            variables = self.variables.to_dict()

        secret_refs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.secret_refs, Unset):
            secret_refs = self.secret_refs.to_dict()

        routes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.routes, Unset):
            routes = self.routes.to_dict()

        policies: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.policies, Unset):
            policies = []
            for policies_item_data in self.policies:
                policies_item = policies_item_data.to_dict()
                policies.append(policies_item)

        queue_bindings: dict[str, Any] | Unset = UNSET
        if not isinstance(self.queue_bindings, Unset):
            queue_bindings = self.queue_bindings.to_dict()

        queue_smoke: dict[str, Any] | Unset = UNSET
        if not isinstance(self.queue_smoke, Unset):
            queue_smoke = self.queue_smoke.to_dict()

        job_smoke: dict[str, Any] | Unset = UNSET
        if not isinstance(self.job_smoke, Unset):
            job_smoke = self.job_smoke.to_dict()

        schedule: dict[str, Any] | Unset = UNSET
        if not isinstance(self.schedule, Unset):
            schedule = self.schedule.to_dict()

        queue_recoveries: dict[str, Any] | Unset = UNSET
        if not isinstance(self.queue_recoveries, Unset):
            queue_recoveries = self.queue_recoveries.to_dict()

        service_bindings: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_bindings, Unset):
            service_bindings = self.service_bindings.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if app is not UNSET:
            field_dict["app"] = app
        if source is not UNSET:
            field_dict["source"] = source
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if variables is not UNSET:
            field_dict["variables"] = variables
        if secret_refs is not UNSET:
            field_dict["secret_refs"] = secret_refs
        if routes is not UNSET:
            field_dict["routes"] = routes
        if policies is not UNSET:
            field_dict["policies"] = policies
        if queue_bindings is not UNSET:
            field_dict["queue_bindings"] = queue_bindings
        if queue_smoke is not UNSET:
            field_dict["queue_smoke"] = queue_smoke
        if job_smoke is not UNSET:
            field_dict["job_smoke"] = job_smoke
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if queue_recoveries is not UNSET:
            field_dict["queue_recoveries"] = queue_recoveries
        if service_bindings is not UNSET:
            field_dict["service_bindings"] = service_bindings

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_manifest import AppManifest
        from ..models.environment_job_schedule import EnvironmentJobSchedule
        from ..models.environment_job_smoke import EnvironmentJobSmoke
        from ..models.environment_policy import EnvironmentPolicy
        from ..models.environment_route_contract import EnvironmentRouteContract
        from ..models.environment_workload_queue_bindings import EnvironmentWorkloadQueueBindings
        from ..models.environment_workload_queue_recoveries import EnvironmentWorkloadQueueRecoveries
        from ..models.environment_workload_queue_smoke import EnvironmentWorkloadQueueSmoke
        from ..models.environment_workload_secret_refs import EnvironmentWorkloadSecretRefs
        from ..models.environment_workload_service_bindings import EnvironmentWorkloadServiceBindings
        from ..models.environment_workload_source import EnvironmentWorkloadSource
        from ..models.environment_workload_variables import EnvironmentWorkloadVariables

        d = dict(src_dict)
        app = d.pop("app", UNSET)

        _source = d.pop("source", UNSET)
        source: EnvironmentWorkloadSource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = EnvironmentWorkloadSource.from_dict(_source)

        _runtime = d.pop("runtime", UNSET)
        runtime: AppManifest | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = AppManifest.from_dict(_runtime)

        _variables = d.pop("variables", UNSET)
        variables: EnvironmentWorkloadVariables | Unset
        if isinstance(_variables, Unset):
            variables = UNSET
        else:
            variables = EnvironmentWorkloadVariables.from_dict(_variables)

        _secret_refs = d.pop("secret_refs", UNSET)
        secret_refs: EnvironmentWorkloadSecretRefs | Unset
        if isinstance(_secret_refs, Unset):
            secret_refs = UNSET
        else:
            secret_refs = EnvironmentWorkloadSecretRefs.from_dict(_secret_refs)

        _routes = d.pop("routes", UNSET)
        routes: EnvironmentRouteContract | Unset
        if isinstance(_routes, Unset):
            routes = UNSET
        else:
            routes = EnvironmentRouteContract.from_dict(_routes)

        _policies = d.pop("policies", UNSET)
        policies: list[EnvironmentPolicy] | Unset = UNSET
        if _policies is not UNSET:
            policies = []
            for policies_item_data in _policies:
                policies_item = EnvironmentPolicy.from_dict(policies_item_data)

                policies.append(policies_item)

        _queue_bindings = d.pop("queue_bindings", UNSET)
        queue_bindings: EnvironmentWorkloadQueueBindings | Unset
        if isinstance(_queue_bindings, Unset):
            queue_bindings = UNSET
        else:
            queue_bindings = EnvironmentWorkloadQueueBindings.from_dict(_queue_bindings)

        _queue_smoke = d.pop("queue_smoke", UNSET)
        queue_smoke: EnvironmentWorkloadQueueSmoke | Unset
        if isinstance(_queue_smoke, Unset):
            queue_smoke = UNSET
        else:
            queue_smoke = EnvironmentWorkloadQueueSmoke.from_dict(_queue_smoke)

        _job_smoke = d.pop("job_smoke", UNSET)
        job_smoke: EnvironmentJobSmoke | Unset
        if isinstance(_job_smoke, Unset):
            job_smoke = UNSET
        else:
            job_smoke = EnvironmentJobSmoke.from_dict(_job_smoke)

        _schedule = d.pop("schedule", UNSET)
        schedule: EnvironmentJobSchedule | Unset
        if isinstance(_schedule, Unset):
            schedule = UNSET
        else:
            schedule = EnvironmentJobSchedule.from_dict(_schedule)

        _queue_recoveries = d.pop("queue_recoveries", UNSET)
        queue_recoveries: EnvironmentWorkloadQueueRecoveries | Unset
        if isinstance(_queue_recoveries, Unset):
            queue_recoveries = UNSET
        else:
            queue_recoveries = EnvironmentWorkloadQueueRecoveries.from_dict(_queue_recoveries)

        _service_bindings = d.pop("service_bindings", UNSET)
        service_bindings: EnvironmentWorkloadServiceBindings | Unset
        if isinstance(_service_bindings, Unset):
            service_bindings = UNSET
        else:
            service_bindings = EnvironmentWorkloadServiceBindings.from_dict(_service_bindings)

        environment_workload = cls(
            app=app,
            source=source,
            runtime=runtime,
            variables=variables,
            secret_refs=secret_refs,
            routes=routes,
            policies=policies,
            queue_bindings=queue_bindings,
            queue_smoke=queue_smoke,
            job_smoke=job_smoke,
            schedule=schedule,
            queue_recoveries=queue_recoveries,
            service_bindings=service_bindings,
        )

        return environment_workload
