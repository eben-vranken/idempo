# Idempo

Go middleware implementing the IETF Idempotency-Key draft RFC with pluggable storage and Stripe-compatible semantics.

`Idempo` is a framework-agnostic HTTP middleware for Go that ensures safe interception, locking, and caching of API calls to avoid duplicate operations during critical mutations like payments or order generation. 

It relies purely on Go's native `net/http` primitives and works seamlessly with `chi`, `gin`, `echo`, or the standard library mux.

## Features

* **IETF & Stripe Semantics:** Fully aligns with draft RFC standards and Stripe-style idempotency behavior.
* **Pluggable Storage:** Flexible backend support depending on your infrastructure:
  * **In-Memory:** For testing or single-instance setups.
  * **Redis:** For distributed, high-throughput environments.
  * **Postgres:** For robust, ACID-compliant persistence.