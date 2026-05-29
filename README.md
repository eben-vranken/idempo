# Idempo

`Idempo` is a framework-agnostic HTTP middleware for Go implementing the IETF Idempotency-Key specification. This middleware ensures safe interception, locking, and caching of API calls in order to avoid duplicates when dealing with critical mutations such as payments or order generation.

It relies purely on Go's native `net/http` primitives and works natively with `chi`, `gin`, `echo`, or even stdlib mux.