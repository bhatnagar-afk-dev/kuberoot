// Reactive client-side state for kubeconfig sources, contexts and connections.
// All data comes from ClusterService; this module only mirrors it.
import { Events } from '@wailsio/runtime';
import { ClusterService } from '../../bindings/kuberoot.in/kuberoot/app';
import type { Snapshot } from '../../bindings/kuberoot.in/kuberoot/app';
import { Phase } from '../../bindings/kuberoot.in/kuberoot/internal/cluster';
import type { Status } from '../../bindings/kuberoot.in/kuberoot/internal/cluster';
import type { Context, Source } from '../../bindings/kuberoot.in/kuberoot/internal/kubeconfig';

export type { Context, Source, Status };
export { Phase };

class ClusterState {
  sources: Source[] = $state([]);
  contexts: Context[] = $state([]);
  statuses: Record<string, Status> = $state({});
  activeId = $state('');
  maxConnections = $state(5);
  loaded = $state(false);
  error = $state('');

  active: Context | undefined = $derived(this.contexts.find((c) => c.id === this.activeId));
  connectedCount = $derived(
    Object.values(this.statuses).filter(
      (s) => s.phase === Phase.PhaseConnected || s.phase === Phase.PhaseConnecting,
    ).length,
  );

  async init() {
    Events.On('clusters:changed', (ev) => this.apply(ev.data));
    Events.On('clusters:status', (ev) => this.setStatus(ev.data));
    this.apply(await ClusterService.Snapshot());
    this.loaded = true;
  }

  private apply(snap: Snapshot) {
    this.sources = snap.sources ?? [];
    this.contexts = snap.contexts ?? [];
    this.maxConnections = snap.maxConnections;
    this.activeId = snap.activeContext;
    const next: Record<string, Status> = {};
    for (const s of snap.statuses ?? []) next[s.contextId] = s;
    this.statuses = next;
  }

  private setStatus(s: Status) {
    this.statuses[s.contextId] = s;
  }

  status(id: string): Status {
    return this.statuses[id] ?? { contextId: id, phase: Phase.PhaseDisconnected, checkedAt: '' };
  }

  async select(id: string) {
    this.activeId = id;
    await ClusterService.SetActive(id);
    const phase = this.status(id).phase;
    if (phase === Phase.PhaseDisconnected) await this.connect(id);
  }

  async connect(id: string) {
    this.setStatus(await ClusterService.Connect(id));
  }

  async disconnect(id: string) {
    await ClusterService.Disconnect(id);
  }

  async pickSource(folder: boolean) {
    this.error = '';
    try {
      await ClusterService.PickSource(folder);
    } catch (e) {
      this.error = errorMessage(e);
    }
  }

  async removeSource(path: string) {
    this.error = '';
    try {
      await ClusterService.RemoveSource(path);
    } catch (e) {
      this.error = errorMessage(e);
    }
  }

  async showSource(path: string) {
    this.error = '';
    try {
      await ClusterService.ShowSource(path);
    } catch (e) {
      this.error = errorMessage(e);
    }
  }
}

function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

/** Shortens a path for display by replacing the home directory with "~". */
export function shortPath(p: string): string {
  return p.replace(/^(\/Users\/[^/]+|\/home\/[^/]+|[A-Za-z]:\\Users\\[^\\]+)/, '~');
}

export const clusters = new ClusterState();
