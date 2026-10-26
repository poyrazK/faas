from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_trial_response import PlatformTenantTrialResponse
from ...models.problem import Problem
from ...models.put_api_consumer_trial_request import PutAPIConsumerTrialRequest
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: PutAPIConsumerTrialRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/platform-tenants/{id}/trial".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantTrialResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantTrialResponse.from_dict(response.json())

        return response_200

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantTrialResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerTrialRequest,
) -> Response[PlatformTenantTrialResponse | Problem]:
    """Start a tenant's one free trial, or move the end of a running one.

     The tenant's usage in the trial is priced at zero and consumes no
    included units, and a month that starts during it has no base fee or
    minimum. A new trial starts at the next UTC minute; a running trial's
    end can move, never before the next minute. A tenant whose trial has
    ended cannot get another (409).

    Args:
        id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantTrialResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerTrialRequest,
) -> PlatformTenantTrialResponse | Problem | None:
    """Start a tenant's one free trial, or move the end of a running one.

     The tenant's usage in the trial is priced at zero and consumes no
    included units, and a month that starts during it has no base fee or
    minimum. A new trial starts at the next UTC minute; a running trial's
    end can move, never before the next minute. A tenant whose trial has
    ended cannot get another (409).

    Args:
        id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantTrialResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerTrialRequest,
) -> Response[PlatformTenantTrialResponse | Problem]:
    """Start a tenant's one free trial, or move the end of a running one.

     The tenant's usage in the trial is priced at zero and consumes no
    included units, and a month that starts during it has no base fee or
    minimum. A new trial starts at the next UTC minute; a running trial's
    end can move, never before the next minute. A tenant whose trial has
    ended cannot get another (409).

    Args:
        id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantTrialResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerTrialRequest,
) -> PlatformTenantTrialResponse | Problem | None:
    """Start a tenant's one free trial, or move the end of a running one.

     The tenant's usage in the trial is priced at zero and consumes no
    included units, and a month that starts during it has no base fee or
    minimum. A new trial starts at the next UTC minute; a running trial's
    end can move, never before the next minute. A tenant whose trial has
    ended cannot get another (409).

    Args:
        id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantTrialResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
