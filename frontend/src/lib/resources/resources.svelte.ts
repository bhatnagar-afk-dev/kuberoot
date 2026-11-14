// Reactive state for live resource views. Rows come from ResourceService as
// batched events; this module only mirrors them.
import { Events } from '@wailsio/runtime';
import { untrack } from 'svelte';
import { ResourceService } from '../../../bindings/kuberoot.in/kuberoot/app';
import { ViewPhase } from '../../../bindings/kuberoot.in/kuberoot/internal/resources';
import type {
  Column,
  ObjectRef,
  ResourceType,
  Row,
  RowBatch,
  ViewStatus,
} from '../../../bindings/kuberoot.in/kuberoot/internal/resources';
import { applyBatch } from './rows';

export type { Column, ObjectRef, ResourceType, Row, ViewStatus };
export { ViewPhase };

const views = new Map<string, ResourceView>();
let listening = false;
let seq = 0;
// View IDs must be unique per app session. The backend outlives a page load
// (a webview reload, or a browser tab in server mode), so IDs are prefixed
// per load.
const idPrefix = Math.random().toString(36).slice(2, 10);

function listen() {
  if (listening) return;
  listening = true;
  // Events for closed views (still in flight when Close ran) are dropped.
  Events.On('resources:rows', (ev) => views.get(ev.data.viewId)?.apply(ev.data));
  Events.On('resources:viewstatus', (ev) => views.get(ev.data.viewId)?.setStatus(ev.data));
}

/** What a view shows. */
export interface ViewSpec {
  contextId: string;
  type: ResourceType;
  namespaces: string[]; // empty means all namespaces
}

/**
 * One live table. Call open() when it is shown and close() when it is not.
 * View IDs are chosen here, so events are routed before Open returns.
 */
export class ResourceView {
  columns: Column[] = $state.raw([]);
  rows: Row[] = $state.raw([]);
  status: ViewStatus = $state.raw({ viewId: '', phase: ViewPhase.ViewLoading });

  spec: ViewSpec;

  private id = '';
  private byUID = new Map<string, Row>();
  private publishPending = false;

  constructor(spec: ViewSpec) {
    this.spec = $state.raw(spec);
  }

  /** Starts the view. Rows from an earlier session stay until the new reset. */
  async open() {
    listen();
    this.id = `view-${idPrefix}-${++seq}`;
    views.set(this.id, this);
    this.status = { viewId: this.id, phase: ViewPhase.ViewLoading };
    const id = this.id;
    try {
      await this.start(id);
    } catch (e) {
      if (this.id === id) {
        this.status = { viewId: id, phase: ViewPhase.ViewError, message: errorMessage(e) };
      }
    }
  }

  /** Asks the backend to start view id. */
  protected start(id: string): Promise<void> {
    return ResourceService.Open({ viewId: id, ...this.spec });
  }

  close() {
    if (!this.id) return;
    views.delete(this.id);
    void ResourceService.Close(this.id);
    this.id = '';
  }

  /** Reopens the view, e.g. after the cluster reconnected. */
  async reopen() {
    this.close();
    await this.open();
  }

  /** Shows other namespaces; the current rows stay until the new ones arrive. */
  async setNamespaces(namespaces: string[]) {
    this.spec = { ...this.spec, namespaces };
    await this.reopen();
  }

  apply(batch: RowBatch) {
    if (batch.reset) this.columns = batch.columns ?? [];
    applyBatch(this.byUID, batch);
    // A reset may come in several chunks within one frame: publish once.
    if (this.publishPending) return;
    this.publishPending = true;
    requestAnimationFrame(() => {
      this.publishPending = false;
      this.rows = [...this.byUID.values()];
    });
  }

  setStatus(s: ViewStatus) {
    this.status = s;
  }
}

/** core/v1 events, as listed by the Events tab. */
const eventsType: ResourceType = { group: '', version: 'v1', resource: 'events', kind: 'Event', namespaced: true };

/** The live events about one object, like the Events section of `kubectl describe`. */
export class EventsView extends ResourceView {
  private ref: ObjectRef;

  constructor(ref: ObjectRef) {
    super({
      contextId: ref.contextId,
      type: eventsType,
      namespaces: ref.type.namespaced && ref.namespace ? [ref.namespace] : [],
    });
    this.ref = ref;
  }

  protected override start(id: string): Promise<void> {
    return ResourceService.OpenEvents(id, this.ref);
  }
}

/**
 * Reopens view whenever connected() turns true again: views die with the
 * connection they were opened on. Call during component initialisation.
 */
export function reopenOnReconnect(view: ResourceView, connected: () => boolean) {
  let was = untrack(connected);
  $effect(() => {
    const now = connected();
    untrack(() => {
      if (now && !was) void view.reopen();
      was = now;
    });
  });
}

/** Current time for Age columns, updated every few seconds while used. */
class Clock {
  now = $state(Date.now());
  private users = 0;
  private timer: ReturnType<typeof setInterval> | undefined;

  /** Starts ticking; returns a function that stops this user's ticks. */
  use(): () => void {
    if (this.users++ === 0) {
      this.now = Date.now();
      this.timer = setInterval(() => (this.now = Date.now()), 5000);
    }
    return () => {
      if (--this.users === 0) clearInterval(this.timer);
    };
  }
}

export const clock = new Clock();

function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}
