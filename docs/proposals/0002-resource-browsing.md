# 0002. Resource browsing

- **Status:** Implementable
- **Authors:** @bhatnagar-afk
- **Created:** 2026-11-09
- **Milestone:** M2

## Summary

Once a cluster is connected (proposal [0001](0001-cluster-management.md)), KubeRoot lets the
user browse its resources: a navigation tree of resource types (built-ins and CRDs), live
tables with the same columns as `kubectl get`, a multi-select namespace filter, and a detail
panel with `kubectl describe` output, read-only YAML, and events. It implements
[ADR 0002](../adr/0002-informer-cache-model.md) (lazy per-cluster informer cache, server-side
Table format, batched UI events) and fills in the details that ADR left to this proposal.

## Motivation

Seeing what is running is the core job of a Kubernetes client, and everything after it (logs
in M3, actions in M4) starts from a row in a table. Tables must be live without polling, show
the columns people already know from `kubectl get`, work the same for CRDs, and stay fast on
clusters with thousands of pods.

### Goals

1. A navigation tree of the resource types the connected cluster serves, grouped into familiar
   sections, plus a **Custom Resources** section for CRDs.
2. **Live tables** for any listable resource type, with `kubectl get` columns (including CRD
   `additionalPrinterColumns`), updated incrementally by watches.
3. **Multi-select namespace filter** (one, several, or all namespaces), remembered per context.
4. Client-side text filter and column sorting.
5. A **detail panel** for the selected object with three tabs: **Describe**
   (`kubectl describe` output), **YAML** (read-only), **Events**.
6. An **Events** table for the selected namespaces, like `kubectl get events`.
7. Clear states for loading, empty, RBAC-forbidden (per namespace), and disconnected.
8. Tables with 5,000+ rows stay responsive (scrolling, filtering, live updates).
9. **Replaceable parts.** Describe output and navigation layout are expected to change later
   (our own structured describe view is a separate project). Both sit behind small interfaces
   so swapping them touches one package. See [Design principles](#design-principles).

### Non-goals

- Editing YAML, `apply`, or any write action (M4 and later).
- Logs (M3), exec, port-forward.
- Aggregated views across clusters. The UI still shows one cluster at a time.
- Metrics (CPU/memory columns).
- Our own structured describe view (separate project; this proposal only makes room for it).
- Label/field-selector queries typed by the user (text filter only in M2).

## Proposal

### User experience

```
┌──────────────┬───────────────┬────────────────────────────────────┬─────────────────────────┐
│ Clusters     │ prod-eu ●     │ Pods        [ns: default, web ▾] 🔍 │ web-7d9f…   Describe|YAML|Events │
│  ● kind-dev  │ Workloads     │ NAME        READY STATUS   AGE     │ Name:       web-7d9f…   │
│  ● prod-eu ◀ │  Pods      ◀  │ web-7d9f…   1/1   Running  3d   ◀  │ Namespace:  web         │
│  ○ staging   │  Deployments  │ web-8c1a…   1/1   Running  3d      │ Status:     Running     │
│              │  StatefulSets │ api-55b2…   0/1   Pending  12s     │ ...                     │
│              │ Network       │                                    │ Events:                 │
│              │ Config        │ ⚠ No permission to list pods in    │  Normal  Pulled  2m ... │
│              │ ...           │   "kube-system"                    │                         │
│              │ Custom Res.   │                                    │                         │
│              │  argoproj.io  │                                    │                         │
└──────────────┴───────────────┴────────────────────────────────────┴─────────────────────────┘
```

- When the active context is connected, the main area shows the resource navigation and a
  table. The M1 context detail (server, version, identity, disconnect) moves to a header strip.
- Choosing a resource type opens its table. The first open of a view shows a loading state and
  then fills in. Later changes appear live.
- The namespace picker offers **All namespaces** or any set of namespaces. Cluster-scoped
  resources ignore it. The selection is remembered per context.
- Clicking a row opens the detail panel. The panel follows the object as it changes and shows
  "deleted" if it disappears.
- If listing is forbidden in some selected namespaces, the table shows the rows it can read
  plus a warning naming the namespaces it could not.
- If the connection drops, tables keep their last rows, dim, and show the connection status.
  They resume when M1 reconnects.

## Design details

### Design principles

The implementation follows SOLID, in Go terms:

- **Single responsibility:** discovery, navigation layout, watching, batching, describing and
  YAML rendering are separate types.
- **Open/closed and dependency inversion:** `internal/resources` defines small interfaces
  (`Catalog`, `Navigator`, `Describer`, `ObjectReader`, `RowSource`, `Sink`). Concrete
  implementations live in subpackages and are wired together in one place (`app/`).
  Consumers depend only on the interfaces.
- **Interface segregation:** resources code reaches the M1 connection only through the narrow
  `Connections` interface below, not through `cluster.Manager`.
- **Replaceable dependencies:** `k8s.io/kubectl` is imported by exactly one package
  (`internal/resources/describe/kubectl`). Replacing the describer means adding a package and
  changing one constructor call.

### Package layout

```
internal/resources/
  types.go            ResourceType, NavGroup, ObjectRef, Column, Row, ViewRequest, ...
  interfaces.go       Catalog, Navigator, Describer, ObjectReader, RowSource, Sink, Connections
  browser.go          Browser: facade the app service calls; owns views
  catalog/            discovery-based Catalog
  nav/curated/        curated Navigator (section table is data)
  table/              RowSource over server-side Table (LIST + WATCH adapter)
  cache/              lazy, ref-counted informers per (context, GVR, namespace)
  view/               a view = merged informer subscriptions + batcher
  object/             ObjectReader: GET + YAML rendering + secret masking
  describe/kubectl/   Describer adapter over k8s.io/kubectl/pkg/describe
```

### Core interfaces

```go
// Connections is the slice of cluster.Manager that resources needs.
type Connections interface {
    // Session returns clients for a connected context and a context that is
    // cancelled when the connection is dropped or replaced.
    Session(contextID string) (Session, error)
}

type Session struct {
    Ctx       context.Context
    Config    *rest.Config          // copy
    Dynamic   dynamic.Interface
    Discovery discovery.CachedDiscoveryInterface
    Mapper    meta.RESTMapper
}

// Catalog lists the resource types a cluster serves.
type Catalog interface {
    ResourceTypes(ctx context.Context) ([]ResourceType, error)
    // Changed reports whether the server's resource types differ from the
    // cached set (e.g. a CRD was added). It never updates the cache.
    Changed(ctx context.Context) (bool, error)
    Refresh(ctx context.Context) ([]ResourceType, error)
}

// Navigator arranges resource types into the navigation tree.
type Navigator interface {
    Navigation(types []ResourceType) []NavGroup
}

// Describer produces human-readable describe output for one object.
type Describer interface {
    Describe(ctx context.Context, s Session, ref ObjectRef) (string, error)
}

// ObjectReader fetches one object and renders it for display.
type ObjectReader interface {
    YAML(ctx context.Context, s Session, ref ObjectRef, opts YAMLOptions) (string, error)
}

// RowSource lists and watches one (GVR, namespace) as table rows.
type RowSource interface {
    cache.ListerWatcher // rows are *table.RowObject; see "Table adapter"
}

// Sink receives batched changes for one view.
type Sink interface {
    Send(RowBatch)
    Status(ViewStatus)
}
```

`cluster.Manager` gets a `Session(id)` method beside the existing `Client(id)`. It builds the
dynamic, cached-discovery and REST-mapper clients lazily per connection and returns the
connection's own `ctx`, so everything started for a cluster stops when it disconnects.

### Catalog and navigation

- **`catalog.Discovery`** uses `ServerPreferredResources` (one preferred version per group),
  memory-cached per connection. It drops subresources and types without `list` and `watch`
  verbs. A partial discovery failure (an aggregated API that is down) keeps the groups that
  worked and reports the failed group in the UI.
- **Refresh, not live updates.** The navigation is not updated on its own in M2. Instead,
  `Changed` compares a hash of the current discovery document with the cached one. It runs on
  reconnect, when the user switches to the cluster, every 5 minutes while connected, and when
  opening a view returns `404` (type removed). If the types changed, the UI shows a
  **Refresh** button on the navigation ("Resource types changed"). Clicking it calls
  `Refresh`. Discovery is readable by every authenticated user, so this needs no extra
  permissions (a CRD watch would), and costs one or two small requests. Live updates may come
  after v0.1.
- **`curated.Navigator`** is a pure function over `[]ResourceType`. Section membership is a
  data table keyed by `group/resource`:

  | Section | Resources |
  |---|---|
  | Workloads | pods, deployments, statefulsets, daemonsets, replicasets, jobs, cronjobs |
  | Network | services, endpointslices, ingresses, ingressclasses, networkpolicies, gateways/httproutes (when served) |
  | Config | configmaps, secrets, resourcequotas, limitranges, horizontalpodautoscalers, poddisruptionbudgets, priorityclasses, runtimeclasses |
  | Storage | persistentvolumeclaims, persistentvolumes, storageclasses, volumeattachments, csidrivers |
  | Access | serviceaccounts, roles, rolebindings, clusterroles, clusterrolebindings |
  | Cluster | nodes, namespaces, events, apiservices, customresourcedefinitions, leases, webhooks |
  | Custom Resources | every type not in a `*.k8s.io` or core group, grouped by API group |

  Other built-in types not in the table go to an **Other** section, so nothing served is ever
  hidden. A different layout (user-configurable, favourites, search-first) is a new `Navigator`
  implementation, and the UI renders `[]NavGroup` without knowing section names.

### Table adapter (from ADR 0002)

- **LIST** and **WATCH** use the REST client with
  `Accept: application/json;as=Table;v=v1;g=meta.k8s.io` and `includeObject=Metadata`. The
  server returns a `metav1.Table` whose rows carry their cells plus `PartialObjectMetadata`.
  Watch events each carry a single-row Table.
- `table.Source` turns these into `RowObject{Meta PartialObjectMetadata, Cells []any}`
  values, which implement `runtime.Object`/`metav1.Object`. A standard
  `cache.SharedIndexInformer` (reflector, relist on `410 Gone`, keyed by namespace/name with
  UID in the row) then works unchanged.
- Column definitions come from the LIST response and are stored with the informer. Priority >
  0 columns (the `-o wide` extras) are sent but hidden by default.
- **Fallback:** if a server rejects the Table format (`406`, some aggregated APIs), the source
  requests `PartialObjectMetadataList` and produces Name / Namespace / Age columns.
- **Age:** Table cells are formatted once by the server and go stale. The UI computes
  **Age** from `metadata.creationTimestamp` and refreshes it every few seconds. Other
  server-formatted date columns refresh when the object changes (a known limitation in M2).

### Informer cache and views

- **`cache.Informers`** holds one informer per `(contextID, GVR, namespace)`, where namespace
  `""` means all namespaces. Informers are ref-counted. With no subscribers they stop after an
  **idle timeout of 60s** (so switching back is instant). There is a limit of
  **30 informers per cluster**, and when it is reached the longest-idle ones are stopped first.
  Each informer's context is a child of `Session.Ctx`.
- **A view** is `(contextID, GVR, namespaces[])`. It subscribes to:
  - one all-namespaces informer when "All namespaces" is chosen or the type is
    cluster-scoped;
  - otherwise one informer per selected namespace, with rows merged by UID;
  - but if an all-namespaces informer for that GVR is **already running**, the view filters it
    instead of starting per-namespace ones.

  A `403 Forbidden` on one namespace's informer marks that namespace as forbidden in the view's
  status without failing the others.
- **Batching:** each view buffers upserts and deletes and flushes at most every **100ms**
  (≤10 batches/s). The first batch after sync, or after a relist, is a `reset` carrying the
  full row set, sent in chunks of 1,000 rows.
- **Namespaces** for the picker come from a view on `namespaces`. If that is forbidden, the
  picker offers the context's default namespace and lets the user type names.

### Detail panel

- **Describe:** `describe/kubectl` calls `describe.DescriberFor(groupKind, cfg)` for built-ins
  and falls back to `describe.GenericDescriberFor(mapping, cfg)` (CRDs), with
  `DescriberSettings{ShowEvents: true, ChunkSize: 500}`. The output is plain text shown in a
  monospace view and re-fetched when the object's `resourceVersion` changes (at most once per
  2s).
- **YAML:** `object.Reader` GETs the full object with the dynamic client and renders it with
  `sigs.k8s.io/yaml`. `managedFields` are hidden by default (toggle). See
  [Security](#security-and-rbac) for Secrets.
- **Events tab:** a view on `core/v1` events in the object's namespace with field selector
  `involvedObject.uid=<uid>` (what `kubectl describe` uses; works on every supported server
  version). Same table pipeline, so it is live.
- The **Events** navigation entry is an ordinary resource view on `events`.

### Go<->UI contract

New service `app.ResourceService`, a thin wrapper over `resources.Browser`:

```go
func (s *ResourceService) Navigation(contextID string) ([]resources.NavGroup, error)
func (s *ResourceService) RefreshNavigation(contextID string) ([]resources.NavGroup, error)
func (s *ResourceService) Open(req resources.ViewRequest) (resources.ViewInfo, error) // returns viewId + columns; rows arrive as events
func (s *ResourceService) SetNamespaces(viewID string, namespaces []string) error  // nil/empty = all
func (s *ResourceService) Close(viewID string)
func (s *ResourceService) OpenEvents(ref resources.ObjectRef) (resources.ViewInfo, error)
func (s *ResourceService) Describe(ref resources.ObjectRef) (string, error)
func (s *ResourceService) YAML(ref resources.ObjectRef, opts resources.YAMLOptions) (string, error)
```

Types sent to the UI:

```go
type ResourceType struct {
    Group, Version, Resource, Kind string
    Namespaced bool
}

type NavGroup struct {
    Title string          `json:"title"`
    Items []NavItem       `json:"items"`  // Title + ResourceType; may nest one level (CRD API groups)
}

type ObjectRef struct {
    ContextID string       `json:"contextId"`
    Type      ResourceType `json:"type"`
    Namespace string       `json:"namespace"`
    Name      string       `json:"name"`
}

type Column struct {
    Name     string `json:"name"`
    Type     string `json:"type"`     // integer | number | string | boolean | date
    Priority int    `json:"priority"` // >0 hidden by default
}

type Row struct {
    UID       string `json:"uid"`
    Namespace string `json:"namespace"`
    Name      string `json:"name"`
    Created   time.Time `json:"created"`
    ResourceVersion string `json:"resourceVersion"`
    Cells     []any  `json:"cells"`
}
```

Events (registered with `application.RegisterEvent`):

| Event | Payload | When |
|---|---|---|
| `resources:rows` | `RowBatch{viewId, reset, upserts []Row, deletes []string}` | Batched changes, ≤10/s per view |
| `resources:navstale` | `{contextId}` | `Catalog.Changed` found new or removed resource types |
| `resources:viewstatus` | `ViewStatus{viewId, phase, forbidden []string, reason, message}` | loading → ready, forbidden namespaces, disconnected, error |

`phase` is `loading | ready | stale | error`. `stale` means the connection is down and the
rows are the last known ones.

### Frontend

Under `frontend/src/lib/resources/`:

- `Nav.svelte` renders `NavGroup[]` generically, with no section names in the UI.
- `ResourceTable.svelte` is a fixed-row-height virtualized table written in-house (no new
  dependency), with sort by column type and text filter over name, namespace and cells.
- `NamespacePicker.svelte` is a multi-select with search, plus "All namespaces".
- `DetailPanel.svelte` has the Describe, YAML and Events tabs. Describe and YAML are rendered
  by small components behind a common props shape, so a structured describe view can drop in.
- **Code editor, one point of change.** All code/text display goes through
  `frontend/src/lib/editor/`. The rest of the app imports only `CodeEditor.svelte` from
  there, never an editor library:

  ```ts
  // frontend/src/lib/editor/types.ts
  export interface CodeEditorProps {
    value: string;
    language: 'yaml' | 'text';   // grows as needed (json, shell, ...)
    readOnly: boolean;           // true everywhere in M2
    onChange?: (value: string) => void;
  }
  ```

  `CodeEditor.svelte` re-exports the current implementation, `codemirror/CodeMirrorEditor.svelte`
  (CodeMirror 6 with `@codemirror/lang-yaml`, loaded lazily so it is not in the startup
  bundle). Only `lib/editor/codemirror/` imports `@codemirror/*`. Switching to another editor
  means adding a sibling folder that implements `CodeEditorProps` and changing the one line in
  `CodeEditor.svelte`. M2 uses it read-only for the YAML tab and for Describe output; YAML
  editing later turns on `readOnly: false`.
- `resources.svelte.ts` holds the view store: it applies `RowBatch`es keyed by UID and closes
  views when components unmount.

The "resource browser arrives in M2" placeholder in `ContextDetail.svelte` is replaced.

### Security and RBAC

- No credentials reach the UI. Same rule as M1: only the types above cross the boundary.
- **Secrets in YAML:** `data` and `stringData` values are replaced with `<hidden: N bytes>` by
  default. A **Reveal** toggle re-fetches unmasked. The
  `kubectl.kubernetes.io/last-applied-configuration` annotation is masked on Secrets too,
  because it contains the values in plain text. `kubectl describe` already prints only sizes.
- RBAC is enforced by the API server. KubeRoot does not pre-check with access reviews in M2.
  It shows `403`s per namespace and per type.
- Error details pass through `internal/redact` before reaching the UI or logs.

### Performance

- API load: one LIST + one WATCH per open (type, namespace). Nothing is watched until a view
  opens. Idle informers stop after 60s, with at most 30 per cluster.
- Memory: rows store cells + partial metadata, not full objects (roughly 1–2 KB per row, so
  5k pods ≈ 10 MB).
- UI: batched events, chunked resets, virtualized rendering. Target: 5,000 rows scroll at
  60 fps and a burst of 1,000 changes applies in under 100ms.
- Binary size: `k8s.io/kubectl` adds an estimated 10–15 MB. This will be measured in the
  describe PR and recorded here.

## Test plan

- **`table`:** a fake API server (`httptest`) returning Table JSON for LIST and WATCH. It
  covers row decoding, a single-row watch event, `410 Gone` relist, the `406` fallback, and
  priority columns.
- **`cache` / `view`:** ref-counting, idle stop with an injectable clock, the per-cluster
  limit, reuse of an existing all-namespaces informer, multi-namespace merge, a `403` on one
  namespace, connection-context cancellation, and the batcher (100ms window, reset chunking).
- **`catalog` / `nav/curated`:** discovery fixtures with CRDs, a failed aggregated group, and
  unknown built-ins → Other. Golden tests for the nav tree.
- **`object`:** golden YAML tests, including Secret masking and `last-applied-configuration`.
- **`describe/kubectl`:** a built-in type and a CRD against a fake clientset/dynamic client.
- **`app`:** `ResourceService` with the injected `emit` (same pattern as `ClusterService`).
- **Integration (kind, CI):** create a CRD and instances, open views, then create, update and
  delete objects and assert the batches. A restricted ServiceAccount checks the forbidden
  namespaces. Scale a deployment to 1,000 pods for a load smoke test.
- **Manual:** kind, EKS, and GKE/AKS (carried over from M1 follow-up 1), launched from
  Finder. On kind, 5,000 pods for scroll and update performance on macOS, Windows and Linux.

## Alternatives

- **Typed or dynamic informers storing full objects, with columns computed in Go:** duplicates
  kubectl's printers, misses CRD printer columns, and uses more memory. Rejected in ADR 0002.
- **Write our own describe view now:** a better UX eventually, but a project of its own. M2
  uses kubectl's describers behind `Describer`.
- **Single-select namespace (`-n` / `-A` only):** simpler, but working across a few related namespaces is common.
  The cost is bounded by reusing all-namespaces informers.
- **Purely discovery-driven navigation:** complete but noisy. The curated layout keeps
  everything reachable through Custom Resources and Other.
- **A table library (e.g. TanStack Table):** feature-rich, but a fixed-row-height virtualizer
  is small, and a library would add bundle size and update overhead on hot paths.

## Resolved questions

1. **Catalog refresh:** on demand. KubeRoot detects that the resource types changed and shows
   a Refresh button; it does not update the navigation live. Live updates may come after v0.1.
   See [Catalog and navigation](#catalog-and-navigation).
2. **YAML viewer / editor:** **CodeMirror 6**, used read-only in M2 and in edit mode when YAML
   editing lands, so we build one viewer instead of a highlighter that is later thrown away.
   It sits behind the `lib/editor` module (see [Frontend](#frontend)), so it can be replaced by
   changing one file. Alternatives considered:
   - **Monaco** (the VS Code editor, named in ADR 0001): best Kubernetes YAML tooling via
     `monaco-yaml`, but several MB plus web workers.
   - **Ace:** mature and mid-weight, but less active and with weaker YAML schema tooling.
   - **A plain highlighter** (highlight.js, Prism): lightest, but cannot become an editor.
3. **Idle timeout and informer limit:** 60s idle timeout and at most 30 informers per cluster,
   as constants.
