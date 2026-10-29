from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_retry_evidence_diagnosis import WorkflowRetryEvidenceDiagnosis
from ...types import Response


def _get_kwargs(
    slug: str,
    review_id: UUID,
    action_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/recovery-policy/reviews/{review_id}/actions/{action_id}/evidence".format(
            slug=quote(str(slug), safe=""),
            review_id=quote(str(review_id), safe=""),
            action_id=quote(str(action_id), safe=""),
        ),
    }

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
) -> Response[Problem | WorkflowRetryEvidenceDiagnosis]:
    """Diagnose retained automatic retry evidence

     Read-only exact retry-generation diagnosis with frozen parent and child lineage checks. Missing
    admission history and superseded runs cannot be repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):

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
) -> Problem | WorkflowRetryEvidenceDiagnosis | None:
    """Diagnose retained automatic retry evidence

     Read-only exact retry-generation diagnosis with frozen parent and child lineage checks. Missing
    admission history and superseded runs cannot be repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):

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
    ).parsed


async def asyncio_detailed(
    slug: str,
    review_id: UUID,
    action_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | WorkflowRetryEvidenceDiagnosis]:
    """Diagnose retained automatic retry evidence

     Read-only exact retry-generation diagnosis with frozen parent and child lineage checks. Missing
    admission history and superseded runs cannot be repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):

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
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    review_id: UUID,
    action_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | WorkflowRetryEvidenceDiagnosis | None:
    """Diagnose retained automatic retry evidence

     Read-only exact retry-generation diagnosis with frozen parent and child lineage checks. Missing
    admission history and superseded runs cannot be repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):
        action_id (UUID):

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
        )
    ).parsed
