# 0002. Lazy per-cluster informer cache with batched UI events

- **Status:** Accepted
- **Date:** 2026-10-31

## Context

KubeRoot shows live resource lists across multiple clusters, including CRDs. Polling the API
server is wasteful and laggy; watching everything up front is expensive on large clusters
(thousands of pods) and for users with many contexts.

## Decision

- Use the **dynamic client + discovery** so any resource type, including CRDs, is supported
  without generated typed clients.
- Maintain **one informer per (cluster, GroupVersionResource, namespace scope)**, started
  **lazily** when a view subscribes and stopped after an idle period with no subscribers.
- Request list columns using the **server-side Table format**
  (`Accept: application/json;as=Table;v=v1;g=meta.k8s.io`) so columns match `kubectl get`
  for built-ins and CRDs alike (honouring `additionalPrinterColumns`).
- Fan out changes to the UI as **Wails events, debounced and batched** (target: at most ~10
  batches/second per view) containing upserts and deletes keyed by UID.
- The UI renders lists with a virtualized table so 5k+ rows stay responsive.

## Consequences

- Memory usage scales with what the user is actually looking at, not with cluster size.
- First open of a view incurs one LIST; subsequent updates are incremental.
- Informer lifecycle (subscribe/unsubscribe, idle timeout, auth errors, RBAC "forbidden")
  needs careful testing; this lives in `internal/resources` with fake-client unit tests and
  kind-based integration tests.
- Table-format responses need a small adapter to merge watch events (which return full
  objects) into table rows; details to be specified in the M2 proposal.

## Alternatives considered

- **Polling LIST on an interval:** simple, but laggy and heavy on API servers.
- **Eager watch of all resources:** fast navigation, but high memory and API load.
- **Typed informers only:** no CRD support without codegen.
