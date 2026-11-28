from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_promotion_receipt import DeploymentPromotionReceipt
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    attempt_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/deployment-promotions/{attempt_id}".format(
            slug=quote(str(slug), safe=""),
            attempt_id=quote(str(attempt_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentPromotionReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentPromotionReceipt.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentPromotionReceipt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentPromotionReceipt | Problem]:
    """Read the immutable acceptance receipt for a promotion attempt.

     Requires deployment-read access to the owned app. Returns the original
    candidate and original serving guard even after traffic changes. A 404 means no retained
    receipt is available; it does not establish rejection and must never
    trigger automatic resubmission. Receipts expire when their deployment
    or app is purged. Named environment identities must still exist. This lookup never changes traffic.

    Args:
        slug (str):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentPromotionReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        attempt_id=attempt_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentPromotionReceipt | Problem | None:
    """Read the immutable acceptance receipt for a promotion attempt.

     Requires deployment-read access to the owned app. Returns the original
    candidate and original serving guard even after traffic changes. A 404 means no retained
    receipt is available; it does not establish rejection and must never
    trigger automatic resubmission. Receipts expire when their deployment
    or app is purged. Named environment identities must still exist. This lookup never changes traffic.

    Args:
        slug (str):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentPromotionReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        attempt_id=attempt_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentPromotionReceipt | Problem]:
    """Read the immutable acceptance receipt for a promotion attempt.

     Requires deployment-read access to the owned app. Returns the original
    candidate and original serving guard even after traffic changes. A 404 means no retained
    receipt is available; it does not establish rejection and must never
    trigger automatic resubmission. Receipts expire when their deployment
    or app is purged. Named environment identities must still exist. This lookup never changes traffic.

    Args:
        slug (str):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentPromotionReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        attempt_id=attempt_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentPromotionReceipt | Problem | None:
    """Read the immutable acceptance receipt for a promotion attempt.

     Requires deployment-read access to the owned app. Returns the original
    candidate and original serving guard even after traffic changes. A 404 means no retained
    receipt is available; it does not establish rejection and must never
    trigger automatic resubmission. Receipts expire when their deployment
    or app is purged. Named environment identities must still exist. This lookup never changes traffic.

    Args:
        slug (str):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentPromotionReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            attempt_id=attempt_id,
            client=client,
        )
    ).parsed
