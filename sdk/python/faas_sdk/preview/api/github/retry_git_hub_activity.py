from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.git_hub_activity_retry_response import GitHubActivityRetryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/github/activity/retry".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GitHubActivityRetryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GitHubActivityRetryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GitHubActivityRetryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Response[GitHubActivityRetryResponse | Problem]:
    """Retry recent failed GitHub activity for an app.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; requeues up to 10 recent dead webhook deliveries
    and Check Run updates belonging to this account-owned app. The
    response contains aggregate counts only and never exposes queue
    identifiers, webhook payloads, or worker errors. Safe to retry with
    an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GitHubActivityRetryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> GitHubActivityRetryResponse | Problem | None:
    """Retry recent failed GitHub activity for an app.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; requeues up to 10 recent dead webhook deliveries
    and Check Run updates belonging to this account-owned app. The
    response contains aggregate counts only and never exposes queue
    identifiers, webhook payloads, or worker errors. Safe to retry with
    an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GitHubActivityRetryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Response[GitHubActivityRetryResponse | Problem]:
    """Retry recent failed GitHub activity for an app.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; requeues up to 10 recent dead webhook deliveries
    and Check Run updates belonging to this account-owned app. The
    response contains aggregate counts only and never exposes queue
    identifiers, webhook payloads, or worker errors. Safe to retry with
    an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GitHubActivityRetryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> GitHubActivityRetryResponse | Problem | None:
    """Retry recent failed GitHub activity for an app.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; requeues up to 10 recent dead webhook deliveries
    and Check Run updates belonging to this account-owned app. The
    response contains aggregate counts only and never exposes queue
    identifiers, webhook payloads, or worker errors. Safe to retry with
    an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GitHubActivityRetryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
