// Navigation trees and per-context browsing choices (selected type and
// namespaces). Trees come from ResourceService; this module only mirrors them.
import { Events } from '@wailsio/runtime';
import { ResourceService } from '../../../bindings/kuberoot.in/kuberoot/app';
import type {
  NavGroup,
  NavItem,
  Navigation,
  ResourceType,
} from '../../../bindings/kuberoot.in/kuberoot/internal/resources';

export type { NavGroup, NavItem, Navigation };

/** The type shown before the user picks one. */
export const defaultSelection: Selection = {
  title: 'Pods',
  type: { group: '', version: 'v1', resource: 'pods', kind: 'Pod', namespaced: true },
};

/** A resource type chosen in the navigation, with its display title. */
export interface Selection {
  title: string;
  type: ResourceType;
}

/** Identifies a resource type across versions of the tree. */
export function typeKey(t: ResourceType): string {
  return `${t.group}/${t.version}/${t.resource}`;
}

/** The navigation tree of one context. */
export class NavState {
  groups: NavGroup[] = $state.raw([]);
  failed: string[] = $state.raw([]);
  stale = $state(false);
  loading = $state(false);
  error = $state('');

  private seq = 0;

  constructor(readonly contextId: string) {}

  /** Loads the tree; on a later call the server also checks for changes. */
  load(): Promise<void> {
    return this.run(() => ResourceService.Navigation(this.contextId));
  }

  /** Re-reads the cluster's resource types. */
  refresh(): Promise<void> {
    return this.run(() => ResourceService.RefreshNavigation(this.contextId));
  }

  private async run(fetch: () => Promise<Navigation>) {
    const seq = ++this.seq;
    this.loading = true;
    try {
      const nav = await fetch();
      if (seq !== this.seq) return;
      this.groups = nav.groups ?? [];
      this.failed = nav.failed ?? [];
      this.stale = nav.stale ?? false;
      this.error = '';
    } catch (e) {
      if (seq === this.seq) this.error = e instanceof Error ? e.message : String(e);
    } finally {
      if (seq === this.seq) this.loading = false;
    }
  }
}

const navs = new Map<string, NavState>();
let listening = false;

/** Returns the shared navigation state of a context. */
export function navigation(contextId: string): NavState {
  if (!listening) {
    listening = true;
    Events.On('resources:navstale', (ev) => {
      const nav = navs.get(ev.data.contextId);
      if (nav) nav.stale = true;
    });
  }
  let nav = navs.get(contextId);
  if (!nav) {
    nav = new NavState(contextId);
    navs.set(contextId, nav);
  }
  return nav;
}

// Per-context choices. The selected type lasts for the app session; the
// namespaces are saved in settings.
const selections = new Map<string, Selection>();

export function selection(contextId: string): Selection {
  return selections.get(contextId) ?? defaultSelection;
}

export function select(contextId: string, s: Selection) {
  selections.set(contextId, s);
}

/** Returns the namespaces last chosen for a context; empty means all. */
export async function savedNamespaces(contextId: string): Promise<string[]> {
  try {
    return (await ResourceService.SavedNamespaces(contextId)) ?? [];
  } catch {
    return [];
  }
}

export function saveNamespaces(contextId: string, namespaces: string[]) {
  ResourceService.SaveNamespaces(contextId, namespaces).catch((e) =>
    console.error('save namespaces:', e),
  );
}
