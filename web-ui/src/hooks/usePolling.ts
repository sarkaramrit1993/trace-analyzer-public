import { useEffect, useRef, type DependencyList } from 'react';

/**
 * Runs fn immediately and then every intervalMs (once if intervalMs is null) while enabled.
 * A dep change, disable, or unmount aborts the in-flight call's signal; fn should pass the
 * signal to fetch and not apply results once it is aborted. Ticks that land while a call is
 * still running are skipped.
 */
export function usePolling(
  fn: (signal: AbortSignal) => Promise<void>,
  intervalMs: number | null,
  deps: DependencyList,
  enabled = true,
): void {
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    let inFlight = false;

    const tick = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        await fnRef.current(controller.signal);
      } catch (err) {
        if (!controller.signal.aborted) console.error(err);
      } finally {
        inFlight = false;
      }
    };

    tick();
    const id = intervalMs === null ? undefined : setInterval(tick, intervalMs);
    return () => {
      controller.abort();
      clearInterval(id);
    };
  }, [enabled, intervalMs, ...deps]);
}
