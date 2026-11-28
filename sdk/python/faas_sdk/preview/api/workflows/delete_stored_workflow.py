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
    expected_version: int,
    restore_manifest: bool | Unset = False,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["expected_version"] = expected_version

    params["restore_manifest"] = restore_manifest

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/workflows/stored/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
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

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

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
    expected_version: int,
    restore_manifest: bool | Unset = False,
    environment: str | Unset = UNSET,
) -> Response[Any | Problem]:
    """Remove a dashboard definition; restoring YAML ownership requires explicit confirmation.

    Args:
        slug (str):
        name (str):
        expected_version (int):
        restore_manifest (bool | Unset):  Default: False.
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        expected_version=expected_version,
        restore_manifest=restore_manifest,
        environment=environment,
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
    expected_version: int,
    restore_manifest: bool | Unset = False,
    environment: str | Unset = UNSET,
) -> Any | Problem | None:
    """Remove a dashboard definition; restoring YAML ownership requires explicit confirmation.

    Args:
        slug (str):
        name (str):
        expected_version (int):
        restore_manifest (bool | Unset):  Default: False.
        environment (str | Unset):

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
        expected_version=expected_version,
        restore_manifest=restore_manifest,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    expected_version: int,
    restore_manifest: bool | Unset = False,
    environment: str | Unset = UNSET,
) -> Response[Any | Problem]:
    """Remove a dashboard definition; restoring YAML ownership requires explicit confirmation.

    Args:
        slug (str):
        name (str):
        expected_version (int):
        restore_manifest (bool | Unset):  Default: False.
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        expected_version=expected_version,
        restore_manifest=restore_manifest,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    expected_version: int,
    restore_manifest: bool | Unset = False,
    environment: str | Unset = UNSET,
) -> Any | Problem | None:
    """Remove a dashboard definition; restoring YAML ownership requires explicit confirmation.

    Args:
        slug (str):
        name (str):
        expected_version (int):
        restore_manifest (bool | Unset):  Default: False.
        environment (str | Unset):

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
            expected_version=expected_version,
            restore_manifest=restore_manifest,
            environment=environment,
        )
    ).parsed
