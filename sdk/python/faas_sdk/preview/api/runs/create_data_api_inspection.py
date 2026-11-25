from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_data_api_inspection_request import CreateDataAPIInspectionRequest
from ...models.data_api_inspection_response import DataAPIInspectionResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateDataAPIInspectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/data-api-inspections".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DataAPIInspectionResponse | Problem | None:
    if response.status_code == 202:
        response_202 = DataAPIInspectionResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[DataAPIInspectionResponse | Problem]:
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
    body: CreateDataAPIInspectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DataAPIInspectionResponse | Problem]:
    """Inspect a managed Data API binding from its app deployment.

     Admits a read-only task pinned to the selected binding and a single
    current deployment. Requires deployment write and managed PostgreSQL
    read access. Binding identity stays unknown until evidence is checked.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDataAPIInspectionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIInspectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
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
    body: CreateDataAPIInspectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> DataAPIInspectionResponse | Problem | None:
    """Inspect a managed Data API binding from its app deployment.

     Admits a read-only task pinned to the selected binding and a single
    current deployment. Requires deployment write and managed PostgreSQL
    read access. Binding identity stays unknown until evidence is checked.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDataAPIInspectionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIInspectionResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateDataAPIInspectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DataAPIInspectionResponse | Problem]:
    """Inspect a managed Data API binding from its app deployment.

     Admits a read-only task pinned to the selected binding and a single
    current deployment. Requires deployment write and managed PostgreSQL
    read access. Binding identity stays unknown until evidence is checked.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDataAPIInspectionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIInspectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateDataAPIInspectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> DataAPIInspectionResponse | Problem | None:
    """Inspect a managed Data API binding from its app deployment.

     Admits a read-only task pinned to the selected binding and a single
    current deployment. Requires deployment write and managed PostgreSQL
    read access. Binding identity stays unknown until evidence is checked.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDataAPIInspectionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIInspectionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
