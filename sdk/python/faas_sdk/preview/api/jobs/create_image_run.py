from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_image_run_request import CreateImageRunRequest
from ...models.image_run_response import ImageRunResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateImageRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/image-runs",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ImageRunResponse | Problem | None:
    if response.status_code == 201:
        response_201 = ImageRunResponse.from_dict(response.json())

        return response_201

    if response.status_code == 202:
        response_202 = ImageRunResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ImageRunResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateImageRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ImageRunResponse | Problem]:
    """Run one command from an OCI image.

     `docker run`-shaped entry point. The API keeps one platform-managed
    runner job (kind `image_run`) per account and normalized image, so
    the image is pulled once and later runs start from the cached
    artifact. While the image is being pulled the call returns 202
    `pulling`; call again once `GET /v1/jobs/{job}` reports
    `image_materialization_status: ready` to create a one-task run
    (201 `queued`). The command follows Docker's rules: `entrypoint`
    replaces the image ENTRYPOINT (and drops its CMD unless `args` is
    set) and `args` replaces the image CMD. Runs, logs and cancellation
    use `/v1/jobs/{job}/runs`. Free accounts get 402
    `jobs_not_allowed`; the route returns 404 until the operator sets
    `FAAS_IMAGE_RUNS=1`. With `interactive: true` the 201
    response carries a one-time `attach` credential for
    `GET /v1/jobs/{job}/runs/{run_id}/tasks/0/attach` and is never stored
    for Idempotency-Key replay.

    Args:
        idempotency_key (str | Unset):
        body (CreateImageRunRequest): Body of POST /v1/image-runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ImageRunResponse | Problem]
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
    body: CreateImageRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> ImageRunResponse | Problem | None:
    """Run one command from an OCI image.

     `docker run`-shaped entry point. The API keeps one platform-managed
    runner job (kind `image_run`) per account and normalized image, so
    the image is pulled once and later runs start from the cached
    artifact. While the image is being pulled the call returns 202
    `pulling`; call again once `GET /v1/jobs/{job}` reports
    `image_materialization_status: ready` to create a one-task run
    (201 `queued`). The command follows Docker's rules: `entrypoint`
    replaces the image ENTRYPOINT (and drops its CMD unless `args` is
    set) and `args` replaces the image CMD. Runs, logs and cancellation
    use `/v1/jobs/{job}/runs`. Free accounts get 402
    `jobs_not_allowed`; the route returns 404 until the operator sets
    `FAAS_IMAGE_RUNS=1`. With `interactive: true` the 201
    response carries a one-time `attach` credential for
    `GET /v1/jobs/{job}/runs/{run_id}/tasks/0/attach` and is never stored
    for Idempotency-Key replay.

    Args:
        idempotency_key (str | Unset):
        body (CreateImageRunRequest): Body of POST /v1/image-runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ImageRunResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateImageRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ImageRunResponse | Problem]:
    """Run one command from an OCI image.

     `docker run`-shaped entry point. The API keeps one platform-managed
    runner job (kind `image_run`) per account and normalized image, so
    the image is pulled once and later runs start from the cached
    artifact. While the image is being pulled the call returns 202
    `pulling`; call again once `GET /v1/jobs/{job}` reports
    `image_materialization_status: ready` to create a one-task run
    (201 `queued`). The command follows Docker's rules: `entrypoint`
    replaces the image ENTRYPOINT (and drops its CMD unless `args` is
    set) and `args` replaces the image CMD. Runs, logs and cancellation
    use `/v1/jobs/{job}/runs`. Free accounts get 402
    `jobs_not_allowed`; the route returns 404 until the operator sets
    `FAAS_IMAGE_RUNS=1`. With `interactive: true` the 201
    response carries a one-time `attach` credential for
    `GET /v1/jobs/{job}/runs/{run_id}/tasks/0/attach` and is never stored
    for Idempotency-Key replay.

    Args:
        idempotency_key (str | Unset):
        body (CreateImageRunRequest): Body of POST /v1/image-runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ImageRunResponse | Problem]
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
    body: CreateImageRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> ImageRunResponse | Problem | None:
    """Run one command from an OCI image.

     `docker run`-shaped entry point. The API keeps one platform-managed
    runner job (kind `image_run`) per account and normalized image, so
    the image is pulled once and later runs start from the cached
    artifact. While the image is being pulled the call returns 202
    `pulling`; call again once `GET /v1/jobs/{job}` reports
    `image_materialization_status: ready` to create a one-task run
    (201 `queued`). The command follows Docker's rules: `entrypoint`
    replaces the image ENTRYPOINT (and drops its CMD unless `args` is
    set) and `args` replaces the image CMD. Runs, logs and cancellation
    use `/v1/jobs/{job}/runs`. Free accounts get 402
    `jobs_not_allowed`; the route returns 404 until the operator sets
    `FAAS_IMAGE_RUNS=1`. With `interactive: true` the 201
    response carries a one-time `attach` credential for
    `GET /v1/jobs/{job}/runs/{run_id}/tasks/0/attach` and is never stored
    for Idempotency-Key replay.

    Args:
        idempotency_key (str | Unset):
        body (CreateImageRunRequest): Body of POST /v1/image-runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ImageRunResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
