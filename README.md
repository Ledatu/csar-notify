# csar-notify

`csar-notify` is the notification delivery service for the workspace. It accepts notification ingest requests over HTTP, buffers and publishes them to RabbitMQ, consumes queued events, dispatches them through enabled providers, stores inbox state in Postgres, and fans out real-time updates over SSE using Redis pub/sub.

## Architecture

- HTTP ingress: `POST /ingest` validates notification batches and pushes them into the in-process buffer.
- Queueing: buffered notifications are published to the RabbitMQ queue from `consumer.queue.name`; failed deliveries can be routed to `consumer.dlq.name`.
- Delivery: the consumer reads batches from RabbitMQ and dispatches through the enabled provider registry.
- Persistence: Postgres stores inbox items and notification preferences.
- Realtime: Redis pub/sub is used to fan out per-subject updates to `GET /notifications/stream`, with optional named site targets when one notify instance serves multiple frontends.
- Health and metrics: a separate plain HTTP sidecar listens on `service.health_port` and exposes `/health`, `/readiness`, and `/metrics`.

## HTTP Surface

The main service listens on `service.port` (default `8085`) and registers:

- `POST /ingest`
- `GET /notifications`
- `GET /notifications/count`
- `PATCH /notifications/{id}/read`
- `PATCH /notifications/{id}/dismiss`
- `GET /notifications/preferences`
- `PUT /notifications/preferences`
- `GET /notifications/stream`

Inbox, preferences, and SSE endpoints expect gateway identity in request context via `gatewayctx`. If `http.allowed_client_cn` is set, the service also requires a peer client certificate whose common name matches that value.

When multiple site targets are configured, clients can select a stream with `GET /notifications/stream?target=<name>`. Site target selection for delivery is driven by notification metadata (`site_target`) or the site preference config (`{"target":"<name>"}`).

## Configuration Notes

`config.yaml` is the local reference config. Required integration settings are:

- `database.dsn`
- `rabbitmq.url`
- `redis.url`

Key defaults from the implementation:

- `service.name`: `csar-notify`
- `service.port`: `8085`
- `service.health_port`: `9085`
- `redis.prefix`: `notify:`
- `consumer.queue.name`: `notify.events`
- `consumer.dlq.name`: `notify.events.dlq`

Providers are opt-in by config:

- `providers.site.enabled` enables in-site delivery.
- `providers.site.targets` and `providers.site.default_target` let one instance fan out realtime events for multiple named sites.
- `providers.telegram.enabled` enables Telegram delivery.
- `providers.telegram.bot_token` keeps the legacy single-bot shape working.
- `providers.telegram.bots` and `providers.telegram.default_bot` allow multiple named Telegram bots; recipient preference config can select one with `{"chat_id":"...","bot":"main-site"}`, and notification metadata can override with `telegram_bot`.

Tracing can be configured either by `tracing.endpoint` or the `-otlp-endpoint` flag. The `-otlp-insecure` flag enables an insecure OTLP gRPC connection.

## Local Validation

From `csar-notify/`:

```bash
go build ./...
go test ./... -count=1
golangci-lint run ./...
```

To run with the checked-in config shape:

```bash
go run ./cmd/csar-notify --config-file config.yaml
```
