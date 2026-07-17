# Configuration

Apexion reads configuration from three sources, in increasing order of precedence:

1. **Built-in defaults** (shown below).
2. **A YAML file** — `configs/apexion.yaml` by default, or any path passed with
   `-c/--config`. The file is entirely optional.
3. **Environment variables** — any setting can be overridden with an
   `APEXION_`-prefixed variable, replacing dots with underscores. For example,
   `minio.endpoint` becomes `APEXION_MINIO_ENDPOINT` and `server.port` becomes
   `APEXION_SERVER_PORT`.

A `.env` file in the working directory is also convenient for local development;
copy [`.env.example`](../.env.example) to `.env` and edit it.

## Reference

### `server`

| Key | Env | Default | Description |
| --- | --- | --- | --- |
| `server.host` | `APEXION_SERVER_HOST` | `0.0.0.0` | Bind address for the HTTP server. |
| `server.port` | `APEXION_SERVER_PORT` | `8080` | HTTP port. |
| `server.read_timeout` | `APEXION_SERVER_READ_TIMEOUT` | `30s` | Request read timeout. |
| `server.write_timeout` | `APEXION_SERVER_WRITE_TIMEOUT` | `60s` | Response write timeout. |
| `server.shutdown_timeout` | `APEXION_SERVER_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown grace period. |

### `storage`

The embedded DuckDB catalog that holds all Apexion state.

| Key | Env | Default | Description |
| --- | --- | --- | --- |
| `storage.path` | `APEXION_STORAGE_PATH` | `data/apexion.duckdb` | Catalog file path. Use `:memory:` for an ephemeral catalog. |
| `storage.max_open_conns` | `APEXION_STORAGE_MAX_OPEN_CONNS` | `1` | Max concurrent DuckDB connections. |

### `minio`

The default object-store connection. Despite the name it works with any
S3-compatible store (AWS S3, MinIO, Cloudflare R2, SeaweedFS, …). Additional
connections can be created and switched from the **Settings** page.

| Key | Env | Default | Description |
| --- | --- | --- | --- |
| `minio.endpoint` | `APEXION_MINIO_ENDPOINT` | `localhost:9000` | Host:port of the S3 endpoint (no scheme). |
| `minio.access_key` | `APEXION_MINIO_ACCESS_KEY` | `minioadmin` | Access key. |
| `minio.secret_key` | `APEXION_MINIO_SECRET_KEY` | `minioadmin` | Secret key. |
| `minio.use_ssl` | `APEXION_MINIO_USE_SSL` | `false` | Use TLS for the connection. |
| `minio.region` | `APEXION_MINIO_REGION` | `us-east-1` | Region. |

> **On EC2:** leave the keys empty and use an IAM instance profile — enable
> role-based auth on the connection from the Settings page.

### `crawler`

| Key | Env | Default | Description |
| --- | --- | --- | --- |
| `crawler.workers` | `APEXION_CRAWLER_WORKERS` | `8` | Concurrent listing/inference workers. |
| `crawler.rate_limit` | `APEXION_CRAWLER_RATE_LIMIT` | `0` | Object-store ops/sec (`0` = unlimited). |
| `crawler.list_page_size` | `APEXION_CRAWLER_LIST_PAGE_SIZE` | `1000` | Objects per LIST request. |
| `crawler.sample_bytes` | `APEXION_CRAWLER_SAMPLE_BYTES` | `262144` | Max bytes read per file for schema detection. |
| `crawler.checkpoint_every` | `APEXION_CRAWLER_CHECKPOINT_EVERY` | `500` | Objects between resume checkpoints. |
| `crawler.ignore_hidden` | `APEXION_CRAWLER_IGNORE_HIDDEN` | `true` | Skip dot-prefixed files/folders. |
| `crawler.timeout` | `APEXION_CRAWLER_TIMEOUT` | `1h` | Overall crawl timeout. |

### `catalog`

Controls how the crawler groups objects into datasets and names partitions.

| Key | Env | Default | Description |
| --- | --- | --- | --- |
| `catalog.virtual_partition_prefix` | `APEXION_CATALOG_VIRTUAL_PARTITION_PREFIX` | `pt` | Prefix for generated partition columns (`pt0`, `pt1`, …). |
| `catalog.virtual_partition_separator` | `APEXION_CATALOG_VIRTUAL_PARTITION_SEPARATOR` | `` (empty) | Separator between prefix and index. |
| `catalog.discovery.strategy` | `APEXION_CATALOG_DISCOVERY_STRATEGY` | `positional` | Dataset grouping: `positional`, `hive`, or `auto`. |

**Discovery strategies**

- `positional` — bare directories become positional partition columns
  (`pt0`, `pt1`, …); all descendants join one dataset.
- `hive` — only `key=value` directories are treated as partitions; other
  directories become separate datasets.
- `auto` — infer per dataset.

### `log`

| Key | Env | Default | Description |
| --- | --- | --- | --- |
| `log.level` | `APEXION_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `log.pretty` | `APEXION_LOG_PRETTY` | `true` | Human-readable console logs (`false` = JSON). |

## Example

```yaml
server:
  port: 8080

storage:
  path: data/apexion.duckdb

minio:
  endpoint: s3.us-east-1.amazonaws.com
  region: us-east-1
  use_ssl: true

crawler:
  workers: 16
```

The equivalent with environment variables:

```bash
export APEXION_MINIO_ENDPOINT=s3.us-east-1.amazonaws.com
export APEXION_MINIO_REGION=us-east-1
export APEXION_MINIO_USE_SSL=true
export APEXION_CRAWLER_WORKERS=16
apexion serve
```
