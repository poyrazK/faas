from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    environment: str | Unset = UNSET,
    if_workload_revision: int | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(if_workload_revision, Unset):
        headers["If-Workload-Revision"] = str(if_workload_revision)

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/work-policies/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
    name: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
    if_workload_revision: int | Unset = UNSET,
) -> Response[Any | Problem]:
    """Delete a policy after removing event subscription bindings.

     A stage deletion preserves an explicit empty collection and its collection revision clock after the
    last policy is removed. Production producer bindings retain their existing deletion checks.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        if_workload_revision (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        environment=environment,
        if_workload_revision=if_workload_revision,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
    if_workload_revision: int | Unset = UNSET,
) -> Any | Problem | None:
    """Delete a policy after removing event subscription bindings.

     A stage deletion preserves an explicit empty collection and its collection revision clock after the
    last policy is removed. Production producer bindings retain their existing deletion checks.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        if_workload_revision (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        environment=environment,
        if_workload_revision=if_workload_revision,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
    if_workload_revision: int | Unset = UNSET,
) -> Response[Any | Problem]:
    """Delete a policy after removing event subscription bindings.

     A stage deletion preserves an explicit empty collection and its collection revision clock after the
    last policy is removed. Production producer bindings retain their existing deletion checks.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        if_workload_revision (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        environment=environment,
        if_workload_revision=if_workload_revision,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
    if_workload_revision: int | Unset = UNSET,
) -> Any | Problem | None:
    """Delete a policy after removing event subscription bindings.

     A stage deletion preserves an explicit empty collection and its collection revision clock after the
    last policy is removed. Production producer bindings retain their existing deletion checks.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        if_workload_revision (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            environment=environment,
            if_workload_revision=if_workload_revision,
        )
    ).parsed
