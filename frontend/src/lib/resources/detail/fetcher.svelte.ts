// Loads text for a detail tab (describe output, YAML) and reloads it when the
// object changes, at most once per interval.

export const REFRESH_INTERVAL = 2000;

export class Fetcher {
  /** The last loaded text; null until the first load succeeds. */
  text: string | null = $state(null);
  error = $state('');
  loading = $state(false);

  private gen = 0;
  private last = -Infinity; // when the last load started
  private inflight = false;
  private pending: (() => Promise<string>) | null = null;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private interval = REFRESH_INTERVAL) {}

  /** Loads soon: now if the last load was over an interval ago. The latest request wins. */
  refresh(load: () => Promise<string>) {
    this.pending = load;
    this.schedule();
  }

  /** Loads now, ignoring any earlier load still running (e.g. new options). */
  now(load: () => Promise<string>) {
    this.gen++;
    this.cancelTimer();
    this.inflight = false;
    this.pending = null;
    void this.run(load);
  }

  dispose() {
    this.gen++;
    this.cancelTimer();
    this.pending = null;
  }

  private schedule() {
    if (this.inflight || this.timer !== undefined || !this.pending) return;
    const wait = this.last + this.interval - Date.now();
    if (wait > 0) {
      this.timer = setTimeout(() => {
        this.timer = undefined;
        this.schedule();
      }, wait);
      return;
    }
    const load = this.pending;
    this.pending = null;
    void this.run(load);
  }

  private async run(load: () => Promise<string>) {
    const gen = this.gen;
    this.inflight = true;
    this.loading = true;
    this.last = Date.now();
    try {
      const text = await load();
      if (gen !== this.gen) return;
      this.text = text;
      this.error = '';
    } catch (e) {
      if (gen !== this.gen) return;
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      if (gen === this.gen) {
        this.inflight = false;
        this.loading = false;
        this.schedule();
      }
    }
  }

  private cancelTimer() {
    clearTimeout(this.timer);
    this.timer = undefined;
  }
}
