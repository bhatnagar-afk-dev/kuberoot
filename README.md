# KubeRoot

An open-source, cross-platform desktop client for Kubernetes — fast, multi-cluster, and free.

KubeRoot runs natively on **macOS, Windows, and Linux**. The core is written in Go
(using [client-go](https://github.com/kubernetes/client-go)) and the UI is rendered in the
OS webview via [Wails](https://wails.io).

> **Status:** early development (pre-v0.1). Not yet ready for daily use.

## Roadmap to v0.1

| Milestone | Scope |
|-----------|-------|
| M0 | Project foundation: scaffold, CI, governance |
| M1 | Clusters: kubeconfig discovery, context switching, health |
| M2 | Resource browsing: live tables, CRDs, describe/YAML view, events |
| M3 | Logs: streaming, follow, search, download |
| M4 | Basic actions: delete, scale, rollout restart (RBAC-aware) |
| M5 | Release v0.1.0: signed installers for all platforms |

Later: YAML edit/apply, exec terminal, port-forward, multi-cluster views, metrics, Helm.
See [docs/proposals](docs/proposals) for in-flight designs.

## Development

Prerequisites: Go 1.26+, Node.js 20+, and the Wails v3 CLI:

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
wails3 doctor   # verifies platform dependencies (on Linux: libgtk-4-dev, libwebkitgtk-6.0-dev)
```

Common tasks:

```sh
wails3 dev                         # run with hot reload
wails3 build                       # production build into ./bin
go test -race ./...                # Go tests
cd frontend && npm run check       # Svelte/TypeScript type-check
wails3 generate bindings -ts -i    # regenerate the Go<->UI contract after changing app/
```

### Layout

```
app/          Wails-bound services — the Go<->UI contract (keep thin)
internal/     Core logic: kubeconfig, cluster, resources, logs, actions, settings
frontend/     Svelte + TypeScript UI; frontend/bindings is generated, do not edit
build/        Platform packaging assets (icons, plists, installers)
docs/adr/     Architecture Decision Records
docs/proposals/  Design proposals for significant features
```

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) and our
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

[Apache License 2.0](LICENSE)
