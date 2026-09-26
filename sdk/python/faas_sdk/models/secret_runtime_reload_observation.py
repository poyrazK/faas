from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.secret_runtime_reload_observation_application_ack import (
    SecretRuntimeReloadObservationApplicationAck,
    check_secret_runtime_reload_observation_application_ack,
)
from ..models.secret_runtime_reload_observation_application_ack_error_code import (
    SecretRuntimeReloadObservationApplicationAckErrorCode,
    check_secret_runtime_reload_observation_application_ack_error_code,
)
from ..models.secret_runtime_reload_observation_error_code import (
    SecretRuntimeReloadObservationErrorCode,
    check_secret_runtime_reload_observation_error_code,
)
from ..models.secret_runtime_reload_observation_projection import (
    SecretRuntimeReloadObservationProjection,
    check_secret_runtime_reload_observation_projection,
)
from ..models.secret_runtime_reload_observation_reload_support import (
    SecretRuntimeReloadObservationReloadSupport,
    check_secret_runtime_reload_observation_reload_support,
)
from ..models.secret_runtime_reload_observation_runtime_state import (
    SecretRuntimeReloadObservationRuntimeState,
    check_secret_runtime_reload_observation_runtime_state,
)
from ..models.secret_runtime_reload_observation_signal import (
    SecretRuntimeReloadObservationSignal,
    check_secret_runtime_reload_observation_signal,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SecretRuntimeReloadObservation")


@_attrs_define
class SecretRuntimeReloadObservation:
    """Non-sensitive active runtime target and optional guest-init projection/signal outcome plus application-owned reload
    acknowledgement. An application acknowledgement is a self-attestation, not independent verification.

    """

    instance_id: str
    """Authorized active runtime instance ID."""
    runtime_state: SecretRuntimeReloadObservationRuntimeState
    """Current active instance state."""
    reload_support: SecretRuntimeReloadObservationReloadSupport
    """Whether this deployment can participate in live reload; unknown means its image opt-in predates persisted
    metadata."""
    reported: bool
    """Whether this runtime has reported a guest-init outcome. False is unknown, never success."""
    version: int | Unset = UNSET
    """Secret version observed by guest-init; compare with delivery_version to detect stale status. Present only
    when reported is true."""
    projection: SecretRuntimeReloadObservationProjection | Unset = UNSET
    signal: SecretRuntimeReloadObservationSignal | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    error_code: SecretRuntimeReloadObservationErrorCode | Unset = UNSET
    application_ack_version: int | Unset = UNSET
    """Secret version the application claims to have applied; compare with delivery_version, independently of the
    guest-init report version."""
    application_ack: SecretRuntimeReloadObservationApplicationAck | Unset = UNSET
    """Explicit application-owned outcome after rereading and applying FAAS_SECRETS_FILE. Missing means unknown,
    not failed."""
    application_ack_at: datetime.datetime | Unset = UNSET
    application_ack_error_code: SecretRuntimeReloadObservationApplicationAckErrorCode | Unset = UNSET
    """Closed, non-sensitive application-reported failure code."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        instance_id = self.instance_id

        runtime_state: str = self.runtime_state

        reload_support: str = self.reload_support

        reported = self.reported

        version = self.version

        projection: str | Unset = UNSET
        if not isinstance(self.projection, Unset):
            projection = self.projection

        signal: str | Unset = UNSET
        if not isinstance(self.signal, Unset):
            signal = self.signal

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        error_code: str | Unset = UNSET
        if not isinstance(self.error_code, Unset):
            error_code = self.error_code

        application_ack_version = self.application_ack_version

        application_ack: str | Unset = UNSET
        if not isinstance(self.application_ack, Unset):
            application_ack = self.application_ack

        application_ack_at: str | Unset = UNSET
        if not isinstance(self.application_ack_at, Unset):
            application_ack_at = self.application_ack_at.isoformat()

        application_ack_error_code: str | Unset = UNSET
        if not isinstance(self.application_ack_error_code, Unset):
            application_ack_error_code = self.application_ack_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "instance_id": instance_id,
                "runtime_state": runtime_state,
                "reload_support": reload_support,
                "reported": reported,
            }
        )
        if version is not UNSET:
            field_dict["version"] = version
        if projection is not UNSET:
            field_dict["projection"] = projection
        if signal is not UNSET:
            field_dict["signal"] = signal
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if application_ack_version is not UNSET:
            field_dict["application_ack_version"] = application_ack_version
        if application_ack is not UNSET:
            field_dict["application_ack"] = application_ack
        if application_ack_at is not UNSET:
            field_dict["application_ack_at"] = application_ack_at
        if application_ack_error_code is not UNSET:
            field_dict["application_ack_error_code"] = application_ack_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        instance_id = d.pop("instance_id")

        runtime_state = check_secret_runtime_reload_observation_runtime_state(d.pop("runtime_state"))

        reload_support = check_secret_runtime_reload_observation_reload_support(d.pop("reload_support"))

        reported = d.pop("reported")

        version = d.pop("version", UNSET)

        _projection = d.pop("projection", UNSET)
        projection: SecretRuntimeReloadObservationProjection | Unset
        if isinstance(_projection, Unset):
            projection = UNSET
        else:
            projection = check_secret_runtime_reload_observation_projection(_projection)

        _signal = d.pop("signal", UNSET)
        signal: SecretRuntimeReloadObservationSignal | Unset
        if isinstance(_signal, Unset):
            signal = UNSET
        else:
            signal = check_secret_runtime_reload_observation_signal(_signal)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _error_code = d.pop("error_code", UNSET)
        error_code: SecretRuntimeReloadObservationErrorCode | Unset
        if isinstance(_error_code, Unset):
            error_code = UNSET
        else:
            error_code = check_secret_runtime_reload_observation_error_code(_error_code)

        application_ack_version = d.pop("application_ack_version", UNSET)

        _application_ack = d.pop("application_ack", UNSET)
        application_ack: SecretRuntimeReloadObservationApplicationAck | Unset
        if isinstance(_application_ack, Unset):
            application_ack = UNSET
        else:
            application_ack = check_secret_runtime_reload_observation_application_ack(_application_ack)

        _application_ack_at = d.pop("application_ack_at", UNSET)
        application_ack_at: datetime.datetime | Unset
        if isinstance(_application_ack_at, Unset):
            application_ack_at = UNSET
        else:
            application_ack_at = datetime.datetime.fromisoformat(_application_ack_at)

        _application_ack_error_code = d.pop("application_ack_error_code", UNSET)
        application_ack_error_code: SecretRuntimeReloadObservationApplicationAckErrorCode | Unset
        if isinstance(_application_ack_error_code, Unset):
            application_ack_error_code = UNSET
        else:
            application_ack_error_code = check_secret_runtime_reload_observation_application_ack_error_code(
                _application_ack_error_code
            )

        secret_runtime_reload_observation = cls(
            instance_id=instance_id,
            runtime_state=runtime_state,
            reload_support=reload_support,
            reported=reported,
            version=version,
            projection=projection,
            signal=signal,
            observed_at=observed_at,
            error_code=error_code,
            application_ack_version=application_ack_version,
            application_ack=application_ack,
            application_ack_at=application_ack_at,
            application_ack_error_code=application_ack_error_code,
        )

        secret_runtime_reload_observation.additional_properties = d
        return secret_runtime_reload_observation

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
