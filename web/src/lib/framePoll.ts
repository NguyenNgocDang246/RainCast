/** RainViewer adds a radar frame every 10 minutes, at :00, :10, … (pipeline.FrameInterval). */
export const FRAME_MS = 10 * 60_000;
/** How often to ask once the next frame is due. */
const DUE_MS = 30_000;
/** How often to ask when the next frame is long overdue. */
const IDLE_MS = 2 * 60_000;
/** Clients wake up to this much after the due time, so they don't all ask in the same second. */
const JITTER_MS = 10_000;

/**
 * A radar frame as an answer names it: its time and, from the server, when
 * the next one is expected and whether a newer one is already out.
 */
export type FrameInfo = { time: string; next_due?: string; outdated?: boolean };

/** When the frame after `f` is expected; a frame's time plus FRAME_MS when the server doesn't say. */
export function dueOf(f: FrameInfo): number {
  const due = f.next_due ? Date.parse(f.next_due) : NaN;
  return Number.isNaN(due) ? Date.parse(f.time) + FRAME_MS : due;
}

/**
 * Milliseconds until the next poll: quiet until the next frame is due,
 * every DUE_MS from then, and IDLE_MS once it is a whole frame late (the
 * source is stuck).
 */
export function nextDelay(due: number, now: number, jitter = Math.random() * JITTER_MS): number {
  if (now < due) return due - now + jitter;
  if (now < due + FRAME_MS) return DUE_MS;
  return IDLE_MS;
}

/**
 * Calls load now and then whenever a newer radar frame may exist. load
 * resolves to the frame it got, or null when it failed. Polling pauses
 * while the page is hidden and catches up when it shows. Returns a function
 * that stops it.
 */
export function pollFrames(load: () => Promise<FrameInfo | null>): () => void {
  /** When the next frame is expected; null until a frame is known. */
  let due: number | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  let loading = false;
  /** The last answer was outdated and was asked again right away. */
  let retried = false;

  const schedule = (now = false) => {
    clearTimeout(timer);
    if (stopped || document.hidden) return;
    timer = setTimeout(run, now ? 0 : due === null ? DUE_MS : nextDelay(due, Date.now()));
  };

  const run = async () => {
    if (loading) return;
    loading = true;
    const f = await load().catch(() => null);
    loading = false;
    if (stopped) return;
    if (f) {
      const d = dueOf(f);
      if (!Number.isNaN(d)) due = d;
    }
    // A newer frame is already out: ask for it once, right away.
    const again = !!f?.outdated && !retried;
    retried = again;
    schedule(again);
  };

  const onVisibility = () => {
    if (document.hidden) clearTimeout(timer);
    // Back after the next frame was due: ask right away.
    else if (due === null || Date.now() >= due) run();
    else schedule();
  };

  document.addEventListener("visibilitychange", onVisibility);
  run();
  return () => {
    stopped = true;
    clearTimeout(timer);
    document.removeEventListener("visibilitychange", onVisibility);
  };
}
