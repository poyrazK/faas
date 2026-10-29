from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    key: str,
    *,
    environment: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/secret-references/{key}".format(
            slug=quote(str(slug), safe=""),
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Response[Any | Problem]:
    """Suppress a primary workload secret destination while preserving the sealed source.

     Requires secrets write permission and the same MFA posture as sealed secret writes. Uses the
    observed catalog identity and the same Git ownership/override contract as PUT. An already suppressed
    unowned destination is an idempotent success. Removing a reference preserves the sealed value and
    records durable suppression of this destination on future cold wakes, including original deployment
    references and automatic delivery. PUT clears that suppression. Suppressed destinations have a
    separate bound of 1024 keys per application across environments and do not consume the variable
    quota.

    Args:
        slug (str):
        key (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Any | Problem | None:
    """Suppress a primary workload secret destination while preserving the sealed source.

     Requires secrets write permission and the same MFA posture as sealed secret writes. Uses the
    observed catalog identity and the same Git ownership/override contract as PUT. An already suppressed
    unowned destination is an idempotent success. Removing a reference preserves the sealed value and
    records durable suppression of this destination on future cold wakes, including original deployment
    references and automatic delivery. PUT clears that suppression. Suppressed destinations have a
    separate bound of 1024 keys per application across environments and do not consume the variable
    quota.

    Args:
        slug (str):
        key (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        key=key,
        client=client,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Response[Any | Problem]:
    """Suppress a primary workload secret destination while preserving the sealed source.

     Requires secrets write permission and the same MFA posture as sealed secret writes. Uses the
    observed catalog identity and the same Git ownership/override contract as PUT. An already suppressed
    unowned destination is an idempotent success. Removing a reference preserves the sealed value and
    records durable suppression of this destination on future cold wakes, including original deployment
    references and automatic delivery. PUT clears that suppression. Suppressed destinations have a
    separate bound of 1024 keys per application across environments and do not consume the variable
    quota.

    Args:
        slug (str):
        key (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Any | Problem | None:
    """Suppress a primary workload secret destination while preserving the sealed source.

     Requires secrets write permission and the same MFA posture as sealed secret writes. Uses the
    observed catalog identity and the same Git ownership/override contract as PUT. An already suppressed
    unowned destination is an idempotent success. Removing a reference preserves the sealed value and
    records durable suppression of this destination on future cold wakes, including original deployment
    references and automatic delivery. PUT clears that suppression. Suppressed destinations have a
    separate bound of 1024 keys per application across environments and do not consume the variable
    quota.

    Args:
        slug (str):
        key (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            key=key,
            client=client,
            environment=environment,
        )
    ).parsed
