from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_outcomes_response import OperationWorkflowOutcomesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    code: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["workflow"] = workflow

    params["limit"] = limit

    params["cursor"] = cursor


    params["code"] = code

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-outcomes",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowOutcomesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowOutcomesResponse.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowOutcomesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    code: str | Unset = UNSET,
) -> Response[OperationWorkflowOutcomesResponse | Problem]:
    """Read completed workflows with explicit outcomes within the authenticated ownership boundary."""

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        limit=limit,
        cursor=cursor,
        code=code,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    code: str | Unset = UNSET,
) -> OperationWorkflowOutcomesResponse | Problem | None:
    """Read completed workflows with explicit outcomes within the authenticated ownership boundary."""

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        limit=limit,
        cursor=cursor,
        code=code,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    code: str | Unset = UNSET,
) -> Response[OperationWorkflowOutcomesResponse | Problem]:
    """Read completed workflows with explicit outcomes within the authenticated ownership boundary."""

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        limit=limit,
        cursor=cursor,
        code=code,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    code: str | Unset = UNSET,
) -> OperationWorkflowOutcomesResponse | Problem | None:
    """Read completed workflows with explicit outcomes within the authenticated ownership boundary."""

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            workflow=workflow,
            limit=limit,
            cursor=cursor,
        code=code,
        )
    ).parsed
