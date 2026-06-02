<h1 align="center">Idempo</h1>

<p align="center">
  Framework-agnostic Go middleware implementing the IETF Idempotency-Key draft RFC.
</p>

<p align="center">
  <a href="https://eben-vranken.github.io/idempo-docs/">📚 Documentation</a>
</p>

<p align="center">
  <a href="https://github.com/eben-vranken/idempo/actions/workflows/ci.yml"><img src="https://github.com/eben-vranken/idempo/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/eben-vranken/idempo"><img src="https://pkg.go.dev/badge/github.com/eben-vranken/idempo.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="MIT License"></a>
</p>

Idempo is a framework-agnostic HTTP middleware for Go that implements the IETF
[Idempotency-Key](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/)
draft with Stripe-compatible semantics. It makes unsafe requests (payments,
order creation, any mutation) safe to retry: a duplicate request runs its side
effect **at most once** and replays the original response.

It is built on Go's standard `net/http` and works with `chi`, `gin`, `echo`, or
the standard library mux.

## How it works

A client sends a unique `Idempotency-Key` header with a request. The middleware:

1. **Claims** the key atomically before the handler runs.
2. If the key is new, it runs your handler, then **stores** the response.
3. If the same key arrives again with the same request, it **replays** the
   stored response (adding `Idempotency-Replayed: true`) without running the
   handler again.
4. If the key is still **in flight**, it returns `409 Conflict`.
5. If the key is reused with a **different** request (method, path, or body),
   it returns `422 Unprocessable Entity`.

Exactly-once execution under concurrent duplicates is enforced by the storage
backend (a mutex in memory, an atomic Lua script in Redis, an
`INSERT ... ON CONFLICT` in Postgres) and verified by tests that fire 50
simultaneous identical requests and assert the handler ran once. The whole
suite runs under the race detector in CI.

## Install

```sh
go get github.com/eben-vranken/idempo
```

## Quick start

```go
package main

import (
	"net/http"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/eben-vranken/idempo/inmem"
)

func main() {
	// lockTTL: how long an in-flight claim is held.
	// retentionTTL: how long a completed response stays replayable.
	store := inmem.New(24*time.Hour, 5*time.Minute)

	mw := idempo.New(store, idempo.Options{})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /charge", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"status":"charged"}`))
	})

	http.ListenAndServe(":8080", mw.Handler(mux))
}
```

Send the same key twice and the second call replays the first response:

```sh
curl -XPOST localhost:8080/charge -H 'Idempotency-Key: 8f3a...' -d '{"amount":42}'
# 201 Created  {"status":"charged"}

curl -XPOST localhost:8080/charge -H 'Idempotency-Key: 8f3a...' -d '{"amount":42}'
# 201 Created  {"status":"charged"}  +  Idempotency-Replayed: true
```

Because `Handler` is a plain `func(http.Handler) http.Handler`, it drops into
any router, e.g. with `chi`:

```go
r := chi.NewRouter()
r.Use(mw.Handler)
```

## Storage backends

All backends implement the `idempo.Store` interface, so you can pick one or
write your own.

```go
// In-memory: single instance or testing.
import "github.com/eben-vranken/idempo/inmem"
store := inmem.New(24*time.Hour, 5*time.Minute)

// Redis: distributed, high throughput.
import (
	"github.com/eben-vranken/idempo/redis"
	goredis "github.com/redis/go-redis/v9"
)
store := redis.New(&goredis.Options{Addr: "localhost:6379"}, 24*time.Hour, 5*time.Minute)

// Postgres: durable, ACID.
import "github.com/eben-vranken/idempo/pg"
if err := pg.RunMigration(connStr); err != nil { /* ... */ }
store, err := pg.New(connStr, 24*time.Hour, 5*time.Minute)
```

### Custom backends

Implement the three-method `Store` interface (`Claim`, `Complete`, `Abandon`).
The contract, including the atomicity guarantee and the fencing-token rule, is
documented on the interface; see the
[Go reference](https://pkg.go.dev/github.com/eben-vranken/idempo#Store).

## Options

```go
idempo.New(store, idempo.Options{
	MaxBodyBytes:      1 << 20,           // request body buffered for fingerprinting (default 1 MiB)
	MaxResponseBytes:  1 << 20,           // response buffered for caching; larger responses pass through uncached (default 1 MiB)
	PersistentTimeout: 10 * time.Second,  // timeout for the store call after the handler returns (default 10s)
	Logger:            slog.Default(),    // structured logging; defaults to slog.Default()
})
```

## Behavior notes

- **Concurrency:** a duplicate that arrives while the first is in flight gets
  `409`, rather than blocking. This matches Stripe's behavior.
- **Failures:** if the store is unavailable at claim time, the request is
  rejected (`500`) and the handler does **not** run (fail closed). If the store
  fails *after* the handler ran, the client still receives its response and the
  failure is logged (fail open).
- **Streaming:** SSE (`http.Flusher`) and connection upgrades
  (`http.Hijacker`, e.g. WebSockets) pass through. A hijacked or oversized
  response is not cached.

## Benchmarks

`go test -bench . -benchmem` against an in-memory store (numbers include request
construction; your handler's own cost dominates in practice):

| Path | ns/op | allocs/op |
|------|------:|----------:|
| Bare handler (no middleware) | ~1926 | 21 |
| Passthrough (no `Idempotency-Key`) | ~2064 | 21 |
| Cached replay | ~4866 | 52 |

The no-key passthrough adds roughly **140 ns and zero extra allocations**; a
cached response is replayed in about **4.9 µs**.

## Alternatives and why this exists

- **Rolling your own with Redis `SETNX`:** easy to get the happy path, hard to
  get right under load. The subtle parts are exactly-once execution when two
  duplicates race, releasing the lock when a handler fails or panics, and
  detecting a key reused with a different payload. Idempo handles all three and
  proves them with concurrency tests run under `-race`.
- **Stripe's server-side approach:** the model Idempo follows, but it is
  internal to Stripe. Idempo gives you the same semantics as standard
  `net/http` middleware, with storage you control.
- **Framework-specific plugins:** tend to lock you into one router. Idempo is
  just `func(http.Handler) http.Handler`, so it composes with any of them.

Idempo aims to be the small, correct, well-tested piece you put in front of a
mutating endpoint and stop thinking about.

## License

MIT. See [LICENSE](LICENSE).
