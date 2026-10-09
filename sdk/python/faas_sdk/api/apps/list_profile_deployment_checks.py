from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_profile_deployment_checks_response import ListProfileDeploymentChecksResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/profiles/deployment-checks".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListProfileDeploymentChecksResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListProfileDeploymentChecksResponse.from_dict(response.json())

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
) -> Response[ListProfileDeploymentChecksResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ListProfileDeploymentChecksResponse | Problem]:
    """List recent automatic CPU-check receipts.

     Returns up to fifty receipts for the owned app with app-read access, newest first. Receipts keep the
    snapshotted policy, exact windows, attempt state and authenticated comparison link. Metadata is
    retained for thirty days; saved investigations retain their existing lifetime and fifty-record
    quota. This read does not query CPU samples.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListProfileDeploymentChecksResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ListProfileDeploymentChecksResponse | Problem | None:
    """List recent automatic CPU-check receipts.

     Returns up to fifty receipts for the owned app with app-read access, newest first. Receipts keep the
    snapshotted policy, exact windows, attempt state and authenticated comparison link. Metadata is
    retained for thirty days; saved investigations retain their existing lifetime and fifty-record
    quota. This read does not query CPU samples.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListProfileDeploymentChecksResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ListProfileDeploymentChecksResponse | Problem]:
    """List recent automatic CPU-check receipts.

     Returns up to fifty receipts for the owned app with app-read access, newest first. Receipts keep the
    snapshotted policy, exact windows, attempt state and authenticated comparison link. Metadata is
    retained for thirty days; saved investigations retain their existing lifetime and fifty-record
    quota. This read does not query CPU samples.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListProfileDeploymentChecksResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ListProfileDeploymentChecksResponse | Problem | None:
    """List recent automatic CPU-check receipts.

     Returns up to fifty receipts for the owned app with app-read access, newest first. Receipts keep the
    snapshotted policy, exact windows, attempt state and authenticated comparison link. Metadata is
    retained for thirty days; saved investigations retain their existing lifetime and fifty-record
    quota. This read does not query CPU samples.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListProfileDeploymentChecksResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
