from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_initial_evidence_repair_request import WorkflowInitialEvidenceRepairRequest
from ...models.workflow_retry_evidence_diagnosis import WorkflowRetryEvidenceDiagnosis
from ...types import Response


def _get_kwargs(
    slug: str,
    review_id: UUID,
    action_id: UUID,
    *,
    body: WorkflowInitialEvidenceRepairRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/recovery-policy/reviews/{review_id}/actions/{action_id}/evidence/parent/repair".format(
            slug=quote(str(slug), safe=""),
            review_id=quote(str(review_id), safe=""),
            action_id=quote(str(action_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRetryEvidenceDiagnosis:
    if response.status_code == 200:
        response_200 = WorkflowRetryEvidenceDiagnosis.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRetryEvidenceDiagnosis]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    review_id: UUID,
    action_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialEvidenceRepairRequest,
) -> Response[Problem | WorkflowRetryEvidenceDiagnosis]:
    """Save verified pending retry parent terminal evidence

     Requires deploy write and MFA. Re-inspects the exact parent generation, item and admission lineage
    under lock. Repair applies only to pending actions and does not authorize child submission. Parent
    and child repair receipts are separate. Saves a missing result once and records the operator reason.
    Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and
    reason returns its receipt; changes conflict. The worker observes repaired proof on its next
    eligible poll.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):
        body (WorkflowInitialEvidenceRepairRequest): Policy-fenced request to repair retained
            initial recovery evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRetryEvidenceDiagnosis]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review_id=review_id,
        action_id=action_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    review_id: UUID,
    action_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialEvidenceRepairRequest,
) -> Problem | WorkflowRetryEvidenceDiagnosis | None:
    """Save verified pending retry parent terminal evidence

     Requires deploy write and MFA. Re-inspects the exact parent generation, item and admission lineage
    under lock. Repair applies only to pending actions and does not authorize child submission. Parent
    and child repair receipts are separate. Saves a missing result once and records the operator reason.
    Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and
    reason returns its receipt; changes conflict. The worker observes repaired proof on its next
    eligible poll.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):
        body (WorkflowInitialEvidenceRepairRequest): Policy-fenced request to repair retained
            initial recovery evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRetryEvidenceDiagnosis
    """

    return sync_detailed(
        slug=slug,
        review_id=review_id,
        action_id=action_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    review_id: UUID,
    action_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialEvidenceRepairRequest,
) -> Response[Problem | WorkflowRetryEvidenceDiagnosis]:
    """Save verified pending retry parent terminal evidence

     Requires deploy write and MFA. Re-inspects the exact parent generation, item and admission lineage
    under lock. Repair applies only to pending actions and does not authorize child submission. Parent
    and child repair receipts are separate. Saves a missing result once and records the operator reason.
    Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and
    reason returns its receipt; changes conflict. The worker observes repaired proof on its next
    eligible poll.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):
        body (WorkflowInitialEvidenceRepairRequest): Policy-fenced request to repair retained
            initial recovery evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRetryEvidenceDiagnosis]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review_id=review_id,
        action_id=action_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    review_id: UUID,
    action_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialEvidenceRepairRequest,
) -> Problem | WorkflowRetryEvidenceDiagnosis | None:
    """Save verified pending retry parent terminal evidence

     Requires deploy write and MFA. Re-inspects the exact parent generation, item and admission lineage
    under lock. Repair applies only to pending actions and does not authorize child submission. Parent
    and child repair receipts are separate. Saves a missing result once and records the operator reason.
    Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and
    reason returns its receipt; changes conflict. The worker observes repaired proof on its next
    eligible poll.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):
        body (WorkflowInitialEvidenceRepairRequest): Policy-fenced request to repair retained
            initial recovery evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRetryEvidenceDiagnosis
    """

    return (
        await asyncio_detailed(
            slug=slug,
            review_id=review_id,
            action_id=action_id,
            client=client,
            body=body,
        )
    ).parsed
