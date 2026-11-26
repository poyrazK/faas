from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_task_response import AppTaskResponse
from ...models.create_data_api_contract_task_output import (
    CreateDataAPIContractTaskOutput,
)
from ...models.create_data_api_contract_task_request import CreateDataAPIContractTaskRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateDataAPIContractTaskRequest,
    output: CreateDataAPIContractTaskOutput | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    json_output: str | Unset = UNSET
    if not isinstance(output, Unset):
        json_output = output

    params["output"] = json_output

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/data-api/contract-tasks".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppTaskResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AppTaskResponse.from_dict(response.json())

        return response_202

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppTaskResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateDataAPIContractTaskRequest,
    output: CreateDataAPIContractTaskOutput | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppTaskResponse | Problem]:
    """Inspect a Data API app's database contract

     Runs one fixed schema inspection inside a fresh task VM from the app's
    live deployment: `types` renders the TypeScript contract, `validators`
    the table and RPC input validators, and `schema` the JSON contract
    snapshot. By default the output arrives in `stdout_tail`. With
    `output=artifact`, the receipt omits the generated output and the
    owner can download up to 8 MiB while the task's deployment is live.
    Belongs to the Data API, so it works without the general app-task API.

    Args:
        slug (str):
        output (CreateDataAPIContractTaskOutput | Unset):
        idempotency_key (str | Unset):
        body (CreateDataAPIContractTaskRequest): The contract inspection to run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        output=output,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateDataAPIContractTaskRequest,
    output: CreateDataAPIContractTaskOutput | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> AppTaskResponse | Problem | None:
    """Inspect a Data API app's database contract

     Runs one fixed schema inspection inside a fresh task VM from the app's
    live deployment: `types` renders the TypeScript contract, `validators`
    the table and RPC input validators, and `schema` the JSON contract
    snapshot. By default the output arrives in `stdout_tail`. With
    `output=artifact`, the receipt omits the generated output and the
    owner can download up to 8 MiB while the task's deployment is live.
    Belongs to the Data API, so it works without the general app-task API.

    Args:
        slug (str):
        output (CreateDataAPIContractTaskOutput | Unset):
        idempotency_key (str | Unset):
        body (CreateDataAPIContractTaskRequest): The contract inspection to run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        output=output,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateDataAPIContractTaskRequest,
    output: CreateDataAPIContractTaskOutput | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppTaskResponse | Problem]:
    """Inspect a Data API app's database contract

     Runs one fixed schema inspection inside a fresh task VM from the app's
    live deployment: `types` renders the TypeScript contract, `validators`
    the table and RPC input validators, and `schema` the JSON contract
    snapshot. By default the output arrives in `stdout_tail`. With
    `output=artifact`, the receipt omits the generated output and the
    owner can download up to 8 MiB while the task's deployment is live.
    Belongs to the Data API, so it works without the general app-task API.

    Args:
        slug (str):
        output (CreateDataAPIContractTaskOutput | Unset):
        idempotency_key (str | Unset):
        body (CreateDataAPIContractTaskRequest): The contract inspection to run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        output=output,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateDataAPIContractTaskRequest,
    output: CreateDataAPIContractTaskOutput | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> AppTaskResponse | Problem | None:
    """Inspect a Data API app's database contract

     Runs one fixed schema inspection inside a fresh task VM from the app's
    live deployment: `types` renders the TypeScript contract, `validators`
    the table and RPC input validators, and `schema` the JSON contract
    snapshot. By default the output arrives in `stdout_tail`. With
    `output=artifact`, the receipt omits the generated output and the
    owner can download up to 8 MiB while the task's deployment is live.
    Belongs to the Data API, so it works without the general app-task API.

    Args:
        slug (str):
        output (CreateDataAPIContractTaskOutput | Unset):
        idempotency_key (str | Unset):
        body (CreateDataAPIContractTaskRequest): The contract inspection to run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            output=output,
            idempotency_key=idempotency_key,
        )
    ).parsed
