import type { ObjectRef } from '../resources.svelte';

/**
 * Props every detail tab takes. A tab is a separate component behind this
 * shape, so another describe view (e.g. a structured one) can replace
 * DescribeTab without touching the panel.
 */
export interface DetailTabProps {
  ref: ObjectRef;
  /** Changes whenever the object does; tabs reload on change. */
  resourceVersion: string;
  /** False while disconnected or after the object was deleted: keep what is shown. */
  live: boolean;
}
