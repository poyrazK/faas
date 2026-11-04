from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_recovery_policy_review import WorkflowRecoveryPolicyReview
from ...types import Response


def _get_kwargs(
    slug: str,
    review_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/recovery-policy/reviews/{review_id}".format(
            slug=quote(str(slug), safe=""),
            review_id=quote(str(review_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRecoveryPolicyReview | None:
    if response.status_code == 200:
        response_200 = WorkflowRecoveryPolicyReview.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRecoveryPolicyReview]:
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
) -> Response[Problem | WorkflowRecoveryPolicyReview]:
    """Inspect one retained policy review

     Reads a frozen policy version and its target decisions. A prepared plan can be exported and
    explicitly applied using generation-fenced controls.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryPolicyReview]
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
) -> Problem | WorkflowRecoveryPolicyReview | None:
    """Inspect one retained policy review

     Reads a frozen policy version and its target decisions. A prepared plan can be exported and
    explicitly applied using generation-fenced controls.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryPolicyReview
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
) -> Response[Problem | WorkflowRecoveryPolicyReview]:
    """Inspect one retained policy review

     Reads a frozen policy version and its target decisions. A prepared plan can be exported and
    explicitly applied using generation-fenced controls.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryPolicyReview]
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
) -> Problem | WorkflowRecoveryPolicyReview | None:
    """Inspect one retained policy review

     Reads a frozen policy version and its target decisions. A prepared plan can be exported and
    explicitly applied using generation-fenced controls.

    Args:
        slug (str):
        review_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryPolicyReview
    """

    return (
        await asyncio_detailed(
            slug=slug,
            review_id=review_id,
            client=client,
        )
    ).parsed
