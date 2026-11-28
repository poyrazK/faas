from typing import Literal

DataUpstreamResponseKind = Literal[
    "cassandra",
    "clickhouse",
    "elasticsearch",
    "etcd",
    "https_api",
    "kafka",
    "memcached",
    "minio",
    "mongo",
    "nats",
    "opensearch",
    "postgres",
    "rabbitmq",
    "redis",
    "s3",
]

DATA_UPSTREAM_RESPONSE_KIND_VALUES: set[DataUpstreamResponseKind] = {
    "cassandra",
    "clickhouse",
    "elasticsearch",
    "etcd",
    "https_api",
    "kafka",
    "memcached",
    "minio",
    "mongo",
    "nats",
    "opensearch",
    "postgres",
    "rabbitmq",
    "redis",
    "s3",
}


def check_data_upstream_response_kind(value: str) -> DataUpstreamResponseKind:
    if value in DATA_UPSTREAM_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_UPSTREAM_RESPONSE_KIND_VALUES!r}")
