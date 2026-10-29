from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.execution_workflow_response import ExecutionWorkflowResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    workflow_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/execution-workflows/{workflow_id}".format(
            workflow_id=quote(str(workflow_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExecutionWorkflowResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ExecutionWorkflowResponse.from_dict(response.json())

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

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExecutionWorkflowResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    workflow_id: str,
    *,
    client: AuthenticatedClient,
) -> Response[ExecutionWorkflowResponse | Problem]:
    """Get workflow status and usage totals.

     Aggregates run counts by lifecycle state and host-measured usage for
    one caller-generated workflow identifier. Runs-only keys see only
    runs created by their own stable key family; broad credentials retain
    account-wide visibility. Importing an artifact from another key family
    does not grant access to that family's run metadata. A server-managed
    workflow includes its continuation status and admitted step count; a
    plan can be visible before its first Run is admitted. Requires
    `apps:read`, `runs:read`, `runs:write`, or `admin`.

    Args:
        workflow_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionWorkflowResponse | Problem]
    """

    kwargs = _get_kwargs(
        workflow_id=workflow_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    workflow_id: str,
    *,
    client: AuthenticatedClient,
) -> ExecutionWorkflowResponse | Problem | None:
    """Get workflow status and usage totals.

     Aggregates run counts by lifecycle state and host-measured usage for
    one caller-generated workflow identifier. Runs-only keys see only
    runs created by their own stable key family; broad credentials retain
    account-wide visibility. Importing an artifact from another key family
    does not grant access to that family's run metadata. A server-managed
    workflow includes its continuation status and admitted step count; a
    plan can be visible before its first Run is admitted. Requires
    `apps:read`, `runs:read`, `runs:write`, or `admin`.

    Args:
        workflow_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionWorkflowResponse | Problem
    """

    return sync_detailed(
        workflow_id=workflow_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    workflow_id: str,
    *,
    client: AuthenticatedClient,
) -> Response[ExecutionWorkflowResponse | Problem]:
    """Get workflow status and usage totals.

     Aggregates run counts by lifecycle state and host-measured usage for
    one caller-generated workflow identifier. Runs-only keys see only
    runs created by their own stable key family; broad credentials retain
    account-wide visibility. Importing an artifact from another key family
    does not grant access to that family's run metadata. A server-managed
    workflow includes its continuation status and admitted step count; a
    plan can be visible before its first Run is admitted. Requires
    `apps:read`, `runs:read`, `runs:write`, or `admin`.

    Args:
        workflow_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionWorkflowResponse | Problem]
    """

    kwargs = _get_kwargs(
        workflow_id=workflow_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    workflow_id: str,
    *,
    client: AuthenticatedClient,
) -> ExecutionWorkflowResponse | Problem | None:
    """Get workflow status and usage totals.

     Aggregates run counts by lifecycle state and host-measured usage for
    one caller-generated workflow identifier. Runs-only keys see only
    runs created by their own stable key family; broad credentials retain
    account-wide visibility. Importing an artifact from another key family
    does not grant access to that family's run metadata. A server-managed
    workflow includes its continuation status and admitted step count; a
    plan can be visible before its first Run is admitted. Requires
    `apps:read`, `runs:read`, `runs:write`, or `admin`.

    Args:
        workflow_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionWorkflowResponse | Problem
    """

    return (
        await asyncio_detailed(
            workflow_id=workflow_id,
            client=client,
        )
    ).parsed
