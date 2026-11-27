import datetime
from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    *,
    expected_updated_at: datetime.datetime,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_expected_updated_at = expected_updated_at.isoformat()
    params["expected_updated_at"] = json_expected_updated_at

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/account/billing-run-schedule/draft",
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
    *,
    client: AuthenticatedClient | Client,
    expected_updated_at: datetime.datetime,
) -> Response[Any | Problem]:
    """Disable the reviewed draft-only schedule.

     Removes the matching draft schedule. Any billing run already started continues.

    Args:
        expected_updated_at (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        expected_updated_at=expected_updated_at,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    expected_updated_at: datetime.datetime,
) -> Any | Problem | None:
    """Disable the reviewed draft-only schedule.

     Removes the matching draft schedule. Any billing run already started continues.

    Args:
        expected_updated_at (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        expected_updated_at=expected_updated_at,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    expected_updated_at: datetime.datetime,
) -> Response[Any | Problem]:
    """Disable the reviewed draft-only schedule.

     Removes the matching draft schedule. Any billing run already started continues.

    Args:
        expected_updated_at (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        expected_updated_at=expected_updated_at,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    expected_updated_at: datetime.datetime,
) -> Any | Problem | None:
    """Disable the reviewed draft-only schedule.

     Removes the matching draft schedule. Any billing run already started continues.

    Args:
        expected_updated_at (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            expected_updated_at=expected_updated_at,
        )
    ).parsed
