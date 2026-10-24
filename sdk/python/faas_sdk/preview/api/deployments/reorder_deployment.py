from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.reorder_deployment_body import ReorderDeploymentBody
from ...models.reorder_deployment_response_200 import ReorderDeploymentResponse200
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ReorderDeploymentBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/reorder".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ReorderDeploymentResponse200 | None:
    if response.status_code == 200:
        response_200 = ReorderDeploymentResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 402:
        response_402 = cast(Any, None)
        return response_402

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ReorderDeploymentResponse200]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReorderDeploymentBody,
) -> Response[Any | ReorderDeploymentResponse200]:
    """Reorder a pending deployment.

     ADR-124 deployment queue controls — update the priority of a
    still-pending deployment. 0 = deploy immediately (top of
    queue), 100 = FIFO default, 1000 = background rebuild.
    Plan-gated (Hobby/Pro/Scale only); Free returns 402
    `plan_reorder_disabled`. 409 if the deployment has already
    moved off the pending queue.

    Args:
        id (str):
        body (ReorderDeploymentBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ReorderDeploymentResponse200]
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReorderDeploymentBody,
) -> Any | ReorderDeploymentResponse200 | None:
    """Reorder a pending deployment.

     ADR-124 deployment queue controls — update the priority of a
    still-pending deployment. 0 = deploy immediately (top of
    queue), 100 = FIFO default, 1000 = background rebuild.
    Plan-gated (Hobby/Pro/Scale only); Free returns 402
    `plan_reorder_disabled`. 409 if the deployment has already
    moved off the pending queue.

    Args:
        id (str):
        body (ReorderDeploymentBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ReorderDeploymentResponse200
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReorderDeploymentBody,
) -> Response[Any | ReorderDeploymentResponse200]:
    """Reorder a pending deployment.

     ADR-124 deployment queue controls — update the priority of a
    still-pending deployment. 0 = deploy immediately (top of
    queue), 100 = FIFO default, 1000 = background rebuild.
    Plan-gated (Hobby/Pro/Scale only); Free returns 402
    `plan_reorder_disabled`. 409 if the deployment has already
    moved off the pending queue.

    Args:
        id (str):
        body (ReorderDeploymentBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ReorderDeploymentResponse200]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReorderDeploymentBody,
) -> Any | ReorderDeploymentResponse200 | None:
    """Reorder a pending deployment.

     ADR-124 deployment queue controls — update the priority of a
    still-pending deployment. 0 = deploy immediately (top of
    queue), 100 = FIFO default, 1000 = background rebuild.
    Plan-gated (Hobby/Pro/Scale only); Free returns 402
    `plan_reorder_disabled`. 409 if the deployment has already
    moved off the pending queue.

    Args:
        id (str):
        body (ReorderDeploymentBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ReorderDeploymentResponse200
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
