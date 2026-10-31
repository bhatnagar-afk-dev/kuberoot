# NNNN. Title

- **Status:** Provisional | Implementable | Implemented | Withdrawn
- **Authors:** @handle
- **Created:** YYYY-MM-DD
- **Tracking issue:** #

## Summary

One paragraph describing the feature.

## Motivation

Why is this needed? What user problem does it solve?

### Goals

- ...

### Non-goals

- ...

## Proposal

User-facing behaviour: what users see and do. Include UI sketches if relevant.

## Design details

### Go<->UI contract

New or changed services/methods in `app/` and events emitted. Show Go signatures.

### Core implementation

Packages under `internal/` affected, data flow, concurrency, error handling.

### Security and RBAC

Credentials handling, permission checks, destructive-action safeguards.

### Performance

Expected load on API servers and memory; behaviour on large clusters.

## Test plan

Unit, integration (kind/envtest), and UI tests. How we verify on macOS, Windows, and Linux.

## Alternatives

Other approaches considered and why they were rejected.

## Open questions

- ...
