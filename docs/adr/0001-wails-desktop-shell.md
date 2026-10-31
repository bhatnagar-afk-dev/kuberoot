# 0001. Use Wails v3 with a Svelte UI for the desktop shell

- **Status:** Accepted
- **Date:** 2026-10-31

## Context

KubeRoot must be a polished desktop app on macOS, Windows, and Linux, with the core written in
Go so it can use client-go directly. The UI needs dense virtualized tables, a code editor for
YAML, and later a terminal emulator — areas where web tooling (Monaco, xterm.js) is far ahead
of native Go toolkits.

## Decision

Use **Wails v3** (v3.0.0 stable) as the desktop shell, with a **Svelte 5 + TypeScript** UI
built by Vite and embedded in the binary.

- Go services in `app/` are bound to the frontend; TypeScript bindings are generated with
  `wails3 generate bindings -ts -i` and committed, forming a typed Go<->UI contract.
- Live updates flow Go -> UI through Wails events.

## Consequences

- Single binary per platform using the OS webview (WKWebView, WebView2, WebKitGTK) — small
  downloads compared with Electron.
- The codebase is roughly 70% Go / 30% TypeScript; contributors to the UI need web skills.
- Rendering differs slightly between webviews; we test on all three platforms in CI.
- Linux uses the GTK4 + WebKitGTK 6.0 stack (Ubuntu 24.04+, Debian 13+, Fedora 40+); older
  distros are unsupported since Wails v3.1 drops the GTK3 fallback. Handled by distro packages.
- Windows requires the WebView2 runtime (bundled bootstrapper in the NSIS installer).

## Alternatives considered

- **Fyne / Gio (pure Go):** single language, but tables, code editing, and terminal widgets
  would need significant custom work and look less native.
- **Wails v2:** mature, but v3 is stable and offers multi-window, typed events, and better
  binding generation.
- **Electron + Go sidecar:** large binaries and two processes to manage.
- **React instead of Svelte:** larger contributor pool, but Svelte gives smaller bundles and
  simpler reactive state for high-frequency updates. Revisit if contributor friction appears.
