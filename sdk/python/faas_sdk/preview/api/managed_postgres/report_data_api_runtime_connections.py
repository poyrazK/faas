from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_api_runtime_connection_report import DataAPIRuntimeConnectionReport
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: DataAPIRuntimeConnectionReport,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/data-api/connection-observations",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
    *,
    client: AuthenticatedClient,
    body: DataAPIRuntimeConnectionReport,
) -> Response[Any | Problem]:
    """Report resident Data API pool configuration

     Private runtime surface. Requires a short-lived gregale:data-api:connections workload assertion for
    the live instance. Owner API keys and application JWTs are not accepted. Reports are reconciled
    against current catalog identity and expire within 30 seconds. This does not reserve connections or
    establish complete demand coverage.

    Args:
        body (DataAPIRuntimeConnectionReport): Private versioned runtime configuration report.
            Instance, deployment, node and database placement authority are resolved from workload
            identity and the catalog, never accepted from this body. observed_at bounds replay;
            unknown invalidates the previous observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: DataAPIRuntimeConnectionReport,
) -> Any | Problem | None:
    """Report resident Data API pool configuration

     Private runtime surface. Requires a short-lived gregale:data-api:connections workload assertion for
    the live instance. Owner API keys and application JWTs are not accepted. Reports are reconciled
    against current catalog identity and expire within 30 seconds. This does not reserve connections or
    establish complete demand coverage.

    Args:
        body (DataAPIRuntimeConnectionReport): Private versioned runtime configuration report.
            Instance, deployment, node and database placement authority are resolved from workload
            identity and the catalog, never accepted from this body. observed_at bounds replay;
            unknown invalidates the previous observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: DataAPIRuntimeConnectionReport,
) -> Response[Any | Problem]:
    """Report resident Data API pool configuration

     Private runtime surface. Requires a short-lived gregale:data-api:connections workload assertion for
    the live instance. Owner API keys and application JWTs are not accepted. Reports are reconciled
    against current catalog identity and expire within 30 seconds. This does not reserve connections or
    establish complete demand coverage.

    Args:
        body (DataAPIRuntimeConnectionReport): Private versioned runtime configuration report.
            Instance, deployment, node and database placement authority are resolved from workload
            identity and the catalog, never accepted from this body. observed_at bounds replay;
            unknown invalidates the previous observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: DataAPIRuntimeConnectionReport,
) -> Any | Problem | None:
    """Report resident Data API pool configuration

     Private runtime surface. Requires a short-lived gregale:data-api:connections workload assertion for
    the live instance. Owner API keys and application JWTs are not accepted. Reports are reconciled
    against current catalog identity and expire within 30 seconds. This does not reserve connections or
    establish complete demand coverage.

    Args:
        body (DataAPIRuntimeConnectionReport): Private versioned runtime configuration report.
            Instance, deployment, node and database placement authority are resolved from workload
            identity and the catalog, never accepted from this body. observed_at bounds replay;
            unknown invalidates the previous observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
