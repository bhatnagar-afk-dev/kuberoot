# 0001. Cluster management

- **Status:** Provisional
- **Authors:** @bhatnagar-afk
- **Created:** 2026-10-31
- **Milestone:** M1

## Summary

KubeRoot discovers the user's kubeconfig files, lists every context they contain, and lets the
user connect to one or more clusters. It reports connection health clearly, including auth
failures from exec credential plugins (aws, gcloud, azure, and similar). This is the base that
resource browsing (M2), logs (M3) and actions (M4) build on.

## Motivation

Users of desktop Kubernetes clients typically have many kubeconfig files and dozens of contexts
across clouds. Existing tools often fail in confusing ways: GUI apps can't find `aws` or
`gke-gcloud-auth-plugin` on macOS, merged kubeconfigs hide duplicate context names, and an
unreachable cluster freezes the UI. Getting this layer right is what makes the rest of the app
feel reliable.

### Goals

1. Discover kubeconfigs automatically: `KUBECONFIG` (from the user's login shell), falling back
   to `~/.kube/config`.
2. Let users add extra kubeconfig **files** and **folders** (non-recursive scan). The choice
   persists across restarts.
3. Show all contexts, grouped by source file, with cluster server, user, default namespace and
   auth type.
4. Connect to and disconnect from contexts. **Multiple contexts may be connected at the same
   time**; the UI shows one at a time in M1.
5. Show live connection health (connecting, connected, or an error with a readable reason),
   with retry.
6. Make exec credential plugins work when the app is launched from Finder, Dock, Start menu or
   a desktop launcher.
7. Pick up kubeconfig changes on disk (e.g. after `aws eks update-kubeconfig`) without a
   restart.

### Non-goals

- **Writing to kubeconfig files**, including `current-context`. Context selection is local to
  KubeRoot; we never modify the user's files.
- Editing or creating contexts, or importing clusters from cloud APIs.
- Aggregated views across multiple clusters (post-v0.1).
- Interactive exec-plugin logins (e.g. a browser SSO prompt). We show the plugin's error and
  tell the user which command to run in a terminal.

## Proposal

### User experience

```
┌────────────────────────┬──────────────────────────────────────────┐
│ KubeRoot               │                                          │
│ 🔍 Filter contexts     │   prod-eu  ● Connected                    │
│                        │   https://ABC.gr7.eu-west-1.eks...        │
│ ~/.kube/config         │   Kubernetes v1.36.4 · user: arn:aws:...  │
│  ● kind-dev            │                                          │
│  ● prod-eu        ◀    │   (resource browser arrives in M2)        │
│  ○ staging             │                                          │
│ ~/.kube/clusters/      │                                          │
│  ⚠ gke-analytics       │                                          │
│                        │                                          │
│ + Add kubeconfig…      │                                          │
└────────────────────────┴──────────────────────────────────────────┘
```

- Clicking a context selects it and connects if it isn't already connected. Status dots:
  grey = disconnected, pulsing = connecting, green = connected, amber = error.
- An error shows a short reason ("Credentials expired", "Cluster unreachable",
  "`aws` not found on PATH") with a **Details** panel for the raw error and plugin stderr, plus
  **Retry**.
- **Add kubeconfig…** opens a native file/folder picker. Sources can be removed from a
  settings view (the default sources can be hidden but not removed).
- The last selected context is restored on launch but **not auto-connected**, so opening the
  app never triggers credential prompts or cloud API calls on its own. An auto-connect setting
  can come later.

## Design details

### Context identity

Context names aren't unique across files: `kind-kind` and `default` appear everywhere.
kubectl merges files with first-wins semantics, which hides duplicates. KubeRoot instead
**loads each file independently**, and identifies a context by:

```
ContextID = sha256(absoluteFilePath + "\x00" + contextName)[:16]   // hex
```

IDs stay stable across restarts and file edits, and are safe to persist in settings. When a
`KUBECONFIG` list references contexts, users, or clusters across files, those cross-file
references are resolved by merging only that list's files, matching kubectl's behaviour.

### Go<->UI contract

New service `app.ClusterService`:

```go
type ClusterService struct{ /* internal/cluster.Manager, internal/kubeconfig.Store, settings */ }

func (s *ClusterService) ListContexts() []kubeconfig.Context
func (s *ClusterService) ListSources() []kubeconfig.Source
func (s *ClusterService) AddSource(path string) (kubeconfig.Source, error)   // file or folder
func (s *ClusterService) PickSource() (*kubeconfig.Source, error)            // native dialog; nil if cancelled
func (s *ClusterService) RemoveSource(path string) error
func (s *ClusterService) Connect(id string) (cluster.Status, error)
func (s *ClusterService) Disconnect(id string) error
func (s *ClusterService) Status(id string) cluster.Status
func (s *ClusterService) SetActive(id string) error                          // persisted selection
```

Types sent to the UI, which contain **no credentials**:

```go
type Context struct {
    ID         string   `json:"id"`
    Name       string   `json:"name"`
    Cluster    string   `json:"cluster"`
    User       string   `json:"user"`
    Namespace  string   `json:"namespace"`   // default namespace, "" means "default"
    Server     string   `json:"server"`
    Source     string   `json:"source"`      // absolute path of the defining file
    AuthType   AuthType `json:"authType"`    // token | cert | basic | exec | authProvider | none
    ExecCmd    string   `json:"execCmd"`     // e.g. "aws", only for exec auth
    IsCurrent  bool     `json:"isCurrent"`   // current-context of its file
}

type Phase string // "disconnected" | "connecting" | "connected" | "error"

type Status struct {
    ContextID     string    `json:"contextId"`
    Phase         Phase     `json:"phase"`
    ServerVersion string    `json:"serverVersion,omitempty"`
    Identity      string    `json:"identity,omitempty"`   // from SelfSubjectReview
    Reason        Reason    `json:"reason,omitempty"`     // machine-readable, see below
    Message       string    `json:"message,omitempty"`    // human-readable summary
    Detail        string    `json:"detail,omitempty"`     // raw error / plugin stderr (secrets redacted)
    CheckedAt     time.Time `json:"checkedAt"`
}
```

`Reason` values: `Unreachable`, `Timeout`, `TLSError`, `Unauthorized`, `Forbidden`,
`ExecPluginNotFound`, `ExecPluginFailed`, `InvalidConfig`, `ConfigChanged`, `Unknown`.

Events (registered with `application.RegisterEvent` so typed TS bindings are generated):

| Event | Payload | When |
|---|---|---|
| `clusters:contexts` | `[]Context` | Sources added or removed, or files changed on disk |
| `clusters:status` | `Status` | Any connection phase change or health-check result |

### Core packages

**`internal/shellenv`**: resolves the user's login-shell environment. On macOS and Linux, GUI
apps don't inherit the terminal's `PATH` or `KUBECONFIG`. At startup we run
`$SHELL -l -i -c 'env -0'` once, with a 5s timeout and stdin closed, and merge `PATH`,
`KUBECONFIG` and cloud-CLI variables (`AWS_*`, `CLOUDSDK_*`, `AZURE_*`, `HTTP(S)_PROXY`,
`NO_PROXY`) into the process environment. On failure we keep the inherited environment and log
a warning. This is a no-op on Windows, and skipped if the app was launched from a terminal
(detected via `TERM`/`TERM_PROGRAM`).

**`internal/kubeconfig`**: source management and parsing.
- `Store` holds the set of sources (default plus user-added), loads each with
  `clientcmd.LoadFromFile`, and produces `[]Context`. Invalid files are reported as an error on
  that source; the rest still load.
- Watches files and folders with `fsnotify`, watching the **parent directory**, since editors
  and cloud CLIs replace files atomically. Changes are debounced by 300ms, reloaded, and
  emitted as one change.
- Builds a `*rest.Config` per context via `clientcmd.NewNonInteractiveClientConfig`, with
  `ExecProvider.InteractiveMode = Never` and QPS 50 / Burst 100 (client-go's defaults of 5/10
  are too low for a UI).

**`internal/cluster`**: the connection manager.
- `Manager` owns `map[ContextID]*Connection` behind a mutex. Each `Connection` holds the
  `rest.Config`, a lazily created `kubernetes.Interface` plus dynamic and discovery clients
  (for M2), its current `Status`, and a cancellable context.
- `Connect` runs **off the UI thread**: phase goes to `connecting`, then
  `GET /version` (10s timeout), then a `SelfSubjectReview` (authentication.k8s.io/v1, GA since
  1.28) to resolve identity and surface auth errors early. A 404 or 403 on SelfSubjectReview
  doesn't fail the connection. The outcome is `connected` or `error`.
- **Health:** while connected, `GET /version` every 30s, retried 3 times with backoff before
  the connection is marked `error`/`Unreachable`. After that, retry backs off up to 2 min, and
  the next success flips it back to connected.
- **Config changes:** if a connected context's kubeconfig entry changes on disk, mark the
  connection `ConfigChanged` and reconnect automatically with the new config.
- **Error classification** maps client-go errors to `Reason`: `*url.Error`/`net.OpError` →
  Unreachable/Timeout, `x509.*` → TLSError, `apierrors.IsUnauthorized` → Unauthorized,
  a missing exec-plugin executable → ExecPluginNotFound (detected from client-go's exec error,
  which doesn't reliably wrap `exec.ErrNotFound`, so we also pre-check with `exec.LookPath`
  on connect; the message includes the command and the resolved PATH), and other exec
  failures → ExecPluginFailed (with stderr).
- `Disconnect` cancels the connection's context; later milestones' informers hang off it.

**`internal/settings`**: persists to `os.UserConfigDir()/KubeRoot/settings.json`
(`~/Library/Application Support` on macOS, `%AppData%` on Windows, `~/.config` on Linux).
Holds extra sources, hidden default sources, and the active context ID. Writes are atomic
(temp file + rename) with 0600 permissions.

### Security

- Credentials never leave Go: the UI only receives the `Context` and `Status` types above.
- `Detail` strings pass through a redactor that removes bearer tokens, `token:` values,
  client-key PEM blocks and URL userinfo before being sent or logged.
- Kubeconfig files are only ever read, never written.
- Exec plugins run with the user's resolved environment, exactly as kubectl would run them.
  Contexts using exec auth display their plugin command so users can see what will execute.

### Performance

- Startup: parsing ~20 kubeconfigs with ~200 contexts should take under 100ms. No network
  calls until the user connects.
- A slow or unreachable cluster never blocks the UI or other connections. Each connection runs
  independently under its own timeout.

## Test plan

- **`internal/kubeconfig`**: table-driven tests over fixture files covering duplicate context
  names across files, a broken YAML file next to valid ones, relative cert paths, a folder
  scan, and a `KUBECONFIG` list with cross-file references. Watcher tests in `t.TempDir()`
  covering atomic replace, delete and recreate, and debounce.
- **`internal/cluster`**: tests against `httptest` servers simulating a healthy cluster, 401,
  403 on SelfSubjectReview, a hanging server (timeout), and a bad TLS certificate. Exec plugin
  tests use a fake plugin binary built in `TestMain` covering success, missing binary and a
  non-zero exit with stderr. Health-check backoff uses an injectable clock.
- **`internal/shellenv`**: parsing of `env -0` output, timeout, and fallback.
- **Redactor**: golden tests for every secret pattern.
- **Integration (CI, Linux)**: a new `integration` job creates a `kind` cluster, connects via
  `cluster.Manager`, and asserts `connected` plus the server version. It runs on PRs that touch
  `internal/`.
- **Manual**: kind locally, plus one EKS or GKE context via exec plugin, with the app launched
  from Finder (not a terminal) to verify `shellenv`.

## Alternatives

- **Merge all kubeconfigs like kubectl:** simpler, but duplicate names become invisible and
  users can't tell which file a context came from.
- **Use `/readyz` for health:** it reflects API-server readiness better, but is often
  forbidden for restricted users. `/version` is readable by `system:public-info-viewer`, and
  SelfSubjectReview validates credentials.
- **Bundle cloud CLIs or reimplement token fetching:** heavy, and it means maintaining every
  cloud's auth. Resolving the shell `PATH` is how other Kubernetes GUIs solve this.
- **Auto-connect on launch:** convenient, but can trigger SSO prompts or cloud API calls
  unexpectedly. Deferred to a setting.

## Open questions

1. Should the folder scan default to also including `~/.kube/*.yaml|*.yml|*config*`? This is a
   common layout and could be on by default.
2. Is up to N simultaneous connections acceptable with no cap in M1, or should we cap it (e.g.
   10) to bound background health checks?
