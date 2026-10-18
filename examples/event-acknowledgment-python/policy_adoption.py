"""Explicit selection and application of an application-owned adoption command."""

from dataclasses import dataclass
from typing import Any

from faas_sdk import (
    EventAcknowledgmentIntentAdoptRetryPolicyOptions,
    EventAcknowledgmentIntentPolicyAdoptionReceipt,
    EventAcknowledgmentIntentRetryPolicy,
    EventAcknowledgmentIntentStatus,
    aadopt_event_acknowledgment_intent_retry_policy,
)


@dataclass(frozen=True, slots=True)
class SavedPolicyAdoption:
    """Retain this metadata under a trusted account/app scope before application."""

    invocation_id: str
    replay_generation: int
    attempt: int
    options: EventAcknowledgmentIntentAdoptRetryPolicyOptions


def select_policy_adoption(
    selected: EventAcknowledgmentIntentStatus, command_id: str
) -> SavedPolicyAdoption:
    """Use the exact timestamp/count from an explicitly selected intent listing."""
    return SavedPolicyAdoption(
        selected.invocation_id,
        selected.replay_generation,
        selected.attempt,
        EventAcknowledgmentIntentAdoptRetryPolicyOptions(
            command_id=command_id,
            expected_updated_at=selected.updated_at,
            expected_delivery_attempts=selected.delivery_attempts,
            retry_policy=EventAcknowledgmentIntentRetryPolicy(1000, 300000),
        ),
    )


async def apply_policy_adoption(
    connection: Any,
    account_id: str,
    app_id: str,
    saved: SavedPolicyAdoption,
    *,
    stop_event: Any = None,
) -> EventAcknowledgmentIntentPolicyAdoptionReceipt:
    """Resolve uncertain results with these original saved inputs; never refresh the fence."""
    return await aadopt_event_acknowledgment_intent_retry_policy(
        connection,
        account_id,
        app_id,
        saved.invocation_id,
        saved.replay_generation,
        saved.attempt,
        saved.options,
        stop_event=stop_event,
    )
