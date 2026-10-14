from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_initial_evidence_diagnosis import WorkflowInitialEvidenceDiagnosis
from ...types import Response


def _get_kwargs(
    slug: str,
    review_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/initial-recovery-reviews/{review_id}/evidence".format(
            slug=quote(str(slug), safe=""),
            review_id=quote(str(review_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowInitialEvidenceDiagnosis:
    if response.status_code == 200:
        response_200 = WorkflowInitialEvidenceDiagnosis.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowInitialEvidenceDiagnosis]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | WorkflowInitialEvidenceDiagnosis]:
    """Diagnose retained initial recovery evidence

     Read-only exact-generation diagnosis. Missing admission history and superseded runs cannot be
    repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialEvidenceDiagnosis]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review_id=review_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | WorkflowInitialEvidenceDiagnosis | None:
    """Diagnose retained initial recovery evidence

     Read-only exact-generation diagnosis. Missing admission history and superseded runs cannot be
    repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialEvidenceDiagnosis
    """

    return sync_detailed(
        slug=slug,
        review_id=review_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | WorkflowInitialEvidenceDiagnosis]:
    """Diagnose retained initial recovery evidence

     Read-only exact-generation diagnosis. Missing admission history and superseded runs cannot be
    repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialEvidenceDiagnosis]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review_id=review_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | WorkflowInitialEvidenceDiagnosis | None:
    """Diagnose retained initial recovery evidence

     Read-only exact-generation diagnosis. Missing admission history and superseded runs cannot be
    repaired. Requires read scope and MFA.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialEvidenceDiagnosis
    """

    return (
        await asyncio_detailed(
            slug=slug,
            review_id=review_id,
            client=client,
        )
    ).parsed
