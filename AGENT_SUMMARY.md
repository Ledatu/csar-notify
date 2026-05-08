# csar-notify Agent Summary

## Role In Prod
Notification delivery service for the CSAR stack. It accepts ingest traffic, buffers to RabbitMQ, consumes and dispatches notifications, stores inbox state in PostgreSQL, and fans out realtime updates over Redis-backed SSE.

## Runtime Entry Points
- `cmd/csar-notify/main.go` starts ingest, consumer, dispatch, SSE hub, HTTP server, and the health sidecar.
- `internal/inbox`, `internal/preferences`, `internal/pushsubscription`, and `internal/sse` own the browser-facing HTTP and realtime surfaces.
- `internal/provider/*` implements delivery backends.

## Trust Boundary
- Browser traffic is expected to arrive through the csar router and is authorized from `gatewayctx`.
- The service uses `gatewayctx.TrustedMiddleware`, so the effective trust boundary depends on router routing and mTLS deployment.
- Prod config keeps `http.allowed_client_cn` empty, so trust is based on the CA and network isolation rather than a pinned client CN.

## Public And Internal Surfaces
- Browser surface: `/notifications`, `/notifications/count`, `/notifications/preferences`, `/notifications/stream`, push subscription endpoints, and VAPID key fetch.
- Internal ingest surface: `POST /ingest`.
- Internal health surface: `/health`, `/readiness`, `/metrics` on the sidecar port.

## Dependencies
- PostgreSQL for inbox, preferences, and subscriptions.
- RabbitMQ for ingest buffering and consumer delivery.
- Redis for SSE fanout and site-target prefixes.
- `csar-core` for config loading, TLS, gateway context, HTTP helpers, health, observability, and pg utilities.

## Audit Hotspots
- Empty `allowed_client_cn` means any internal cert holder can reach the backend if network controls are weak.
- The router config deliberately throttles browser routes, so route drift matters.
- The notification path mixes long-lived SSE, Redis fanout, and provider dispatch, so backpressure and reconnect behavior are the main operational risks.

## First Files To Read
- `README.md`
- `cmd/csar-notify/main.go`
- `internal/config/config.go`
- `internal/httpauth/subject.go`
- `internal/sse/hub.go`
- `internal/provider/telegram/telegram.go`
- `internal/provider/webpush/*`
- `csar-configs/prod/csar-notify/config.yaml`
- `csar-configs/prod/csar/notify/routes.yaml`
- `csar-configs/prod/csar/notify-svc/routes.yaml`

## DRY / Extraction Candidates
- `internal/rmq/*` and `internal/pipeline/*` closely mirror `csar-audit`.
- The router-backed service client and gateway identity plumbing should stay on `csar-core` primitives instead of ad hoc copies.

## Required Quality Gates
- `go build ./...`
- `go test ./... -count=1`
- `golangci-lint run ./...`
