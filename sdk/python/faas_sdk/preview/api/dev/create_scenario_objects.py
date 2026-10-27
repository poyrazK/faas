from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_scenario_objects_request import CreateScenarioObjectsRequest
from ...models.problem import Problem
from ...models.scenario_objects_receipt import ScenarioObjectsReceipt
from ...types import Response


def _get_kwargs(
    run_id: str,
    *,
    body: CreateScenarioObjectsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dev/test-runs/{run_id}/objects".format(
            run_id=quote(str(run_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ScenarioObjectsReceipt | None:
    if response.status_code == 201:
        response_201 = ScenarioObjectsReceipt.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScenarioObjectsReceipt]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioObjectsRequest,
) -> Response[Problem | ScenarioObjectsReceipt]:
    """Create an isolated S3 fixture namespace before deploying a rehearsal workload.

     Disabled by default. Pins bounded fixture bytes in dedicated rehearsal tables and injects run-scoped
    credentials, AWS_REGION, AWS_ENDPOINT_URL_S3 and S3_BUCKET as sealed ephemeral app secrets. The
    private adapter never accesses managed or production buckets. Only the primary contained app and its
    commands receive credentials. Requires no existing deployment and complete contained simulation
    enrollment.

    Args:
        run_id (str):
        body (CreateScenarioObjectsRequest): Fresh primary workload and exact initial objects for
            an isolated rehearsal namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioObjectsReceipt]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioObjectsRequest,
) -> Problem | ScenarioObjectsReceipt | None:
    """Create an isolated S3 fixture namespace before deploying a rehearsal workload.

     Disabled by default. Pins bounded fixture bytes in dedicated rehearsal tables and injects run-scoped
    credentials, AWS_REGION, AWS_ENDPOINT_URL_S3 and S3_BUCKET as sealed ephemeral app secrets. The
    private adapter never accesses managed or production buckets. Only the primary contained app and its
    commands receive credentials. Requires no existing deployment and complete contained simulation
    enrollment.

    Args:
        run_id (str):
        body (CreateScenarioObjectsRequest): Fresh primary workload and exact initial objects for
            an isolated rehearsal namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioObjectsReceipt
    """

    return sync_detailed(
        run_id=run_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioObjectsRequest,
) -> Response[Problem | ScenarioObjectsReceipt]:
    """Create an isolated S3 fixture namespace before deploying a rehearsal workload.

     Disabled by default. Pins bounded fixture bytes in dedicated rehearsal tables and injects run-scoped
    credentials, AWS_REGION, AWS_ENDPOINT_URL_S3 and S3_BUCKET as sealed ephemeral app secrets. The
    private adapter never accesses managed or production buckets. Only the primary contained app and its
    commands receive credentials. Requires no existing deployment and complete contained simulation
    enrollment.

    Args:
        run_id (str):
        body (CreateScenarioObjectsRequest): Fresh primary workload and exact initial objects for
            an isolated rehearsal namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioObjectsReceipt]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateScenarioObjectsRequest,
) -> Problem | ScenarioObjectsReceipt | None:
    """Create an isolated S3 fixture namespace before deploying a rehearsal workload.

     Disabled by default. Pins bounded fixture bytes in dedicated rehearsal tables and injects run-scoped
    credentials, AWS_REGION, AWS_ENDPOINT_URL_S3 and S3_BUCKET as sealed ephemeral app secrets. The
    private adapter never accesses managed or production buckets. Only the primary contained app and its
    commands receive credentials. Requires no existing deployment and complete contained simulation
    enrollment.

    Args:
        run_id (str):
        body (CreateScenarioObjectsRequest): Fresh primary workload and exact initial objects for
            an isolated rehearsal namespace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioObjectsReceipt
    """

    return (
        await asyncio_detailed(
            run_id=run_id,
            client=client,
            body=body,
        )
    ).parsed
