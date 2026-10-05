from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_field_ownership_request import EnvironmentFieldOwnershipRequest
from ...models.environment_field_ownership_response import EnvironmentFieldOwnershipResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: EnvironmentFieldOwnershipRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/environment-field-ownership",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentFieldOwnershipResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentFieldOwnershipResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[EnvironmentFieldOwnershipResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: EnvironmentFieldOwnershipRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[EnvironmentFieldOwnershipResponse | Problem]:
    """Reserve scoped Terraform variable, source or configuration fields against Git adoption.

    Args:
        idempotency_key (str | Unset):
        body (EnvironmentFieldOwnershipRequest): Specify exactly one of app or project. App fields
            use variables/KEY or source; project fields use configuration/KEY. Terraform must claim
            before writes and release after deletion. Claims contain no values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentFieldOwnershipResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: EnvironmentFieldOwnershipRequest,
    idempotency_key: str | Unset = UNSET,
) -> EnvironmentFieldOwnershipResponse | Problem | None:
    """Reserve scoped Terraform variable, source or configuration fields against Git adoption.

    Args:
        idempotency_key (str | Unset):
        body (EnvironmentFieldOwnershipRequest): Specify exactly one of app or project. App fields
            use variables/KEY or source; project fields use configuration/KEY. Terraform must claim
            before writes and release after deletion. Claims contain no values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentFieldOwnershipResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: EnvironmentFieldOwnershipRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[EnvironmentFieldOwnershipResponse | Problem]:
    """Reserve scoped Terraform variable, source or configuration fields against Git adoption.

    Args:
        idempotency_key (str | Unset):
        body (EnvironmentFieldOwnershipRequest): Specify exactly one of app or project. App fields
            use variables/KEY or source; project fields use configuration/KEY. Terraform must claim
            before writes and release after deletion. Claims contain no values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentFieldOwnershipResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: EnvironmentFieldOwnershipRequest,
    idempotency_key: str | Unset = UNSET,
) -> EnvironmentFieldOwnershipResponse | Problem | None:
    """Reserve scoped Terraform variable, source or configuration fields against Git adoption.

    Args:
        idempotency_key (str | Unset):
        body (EnvironmentFieldOwnershipRequest): Specify exactly one of app or project. App fields
            use variables/KEY or source; project fields use configuration/KEY. Terraform must claim
            before writes and release after deletion. Claims contain no values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentFieldOwnershipResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
