import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { usePolling } from './usePolling';

const deferred = <T,>() => {
  let resolve: (value: T) => void = () => {};
  const promise = new Promise<T>(r => { resolve = r; });
  return { promise, resolve };
};

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); });

describe('usePolling', () => {
  it('calls fn immediately on mount', () => {
    const fn = vi.fn(async () => {});
    renderHook(() => usePolling(fn, 1000, []));
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('calls fn again every intervalMs', async () => {
    const fn = vi.fn(async () => {});
    renderHook(() => usePolling(fn, 1000, []));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(fn).toHaveBeenCalledTimes(4);
  });

  it('runs once and does not repeat when intervalMs is null', async () => {
    const fn = vi.fn(async () => {});
    renderHook(() => usePolling(fn, null, []));
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('does not call fn while disabled', async () => {
    const fn = vi.fn(async () => {});
    renderHook(() => usePolling(fn, 1000, [], false));
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(fn).not.toHaveBeenCalled();
  });

  it('skips a tick while the previous call is still in flight', async () => {
    const pending = deferred<void>();
    const fn = vi.fn(() => pending.promise);
    renderHook(() => usePolling(fn, 1000, []));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(fn).toHaveBeenCalledTimes(1);
    await act(async () => { pending.resolve(); await vi.advanceTimersByTimeAsync(1000); });
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it('aborts the in-flight call on dep change so a stale response is not applied', async () => {
    const responses: Record<string, ReturnType<typeof deferred<string>>> = {
      a: deferred<string>(),
      b: deferred<string>(),
    };
    const setter = vi.fn();
    const signals: AbortSignal[] = [];
    const { rerender } = renderHook(({ id }) => usePolling(async signal => {
      signals.push(signal);
      const value = await responses[id].promise;
      if (!signal.aborted) setter(value);
    }, 1000, [id]), { initialProps: { id: 'a' } });

    rerender({ id: 'b' });
    expect(signals[0].aborted).toBe(true);
    expect(signals[1].aborted).toBe(false);

    await act(async () => { responses.b.resolve('b-data'); responses.a.resolve('a-data'); });
    expect(setter).toHaveBeenCalledTimes(1);
    expect(setter).toHaveBeenCalledWith('b-data');
  });

  it('aborts and stops polling on unmount', async () => {
    const signals: AbortSignal[] = [];
    const fn = vi.fn(async (signal: AbortSignal) => { signals.push(signal); });
    const { unmount } = renderHook(() => usePolling(fn, 1000, []));
    unmount();
    expect(signals[0].aborted).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('aborts when disabled after being enabled', () => {
    const signals: AbortSignal[] = [];
    const { rerender } = renderHook(({ enabled }) => usePolling(async signal => { signals.push(signal); }, 1000, [], enabled), {
      initialProps: { enabled: true },
    });
    rerender({ enabled: false });
    expect(signals[0].aborted).toBe(true);
  });

  it('uses the latest fn without restarting the interval', async () => {
    const first = vi.fn(async () => {});
    const second = vi.fn(async () => {});
    const { rerender } = renderHook(({ fn }) => usePolling(fn, 1000, []), { initialProps: { fn: first } });
    rerender({ fn: second });
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(1);
  });

  it('swallows errors from aborted calls without logging', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    const pending = deferred<void>();
    const { unmount } = renderHook(() => usePolling(async signal => {
      await pending.promise;
      signal.throwIfAborted();
    }, 1000, []));
    unmount();
    await act(async () => { pending.resolve(); });
    expect(error).not.toHaveBeenCalled();
    error.mockRestore();
  });

  it('logs errors from live calls', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    renderHook(() => usePolling(async () => { throw new Error('boom'); }, 1000, []));
    await act(async () => {});
    expect(error).toHaveBeenCalledWith(expect.objectContaining({ message: 'boom' }));
    error.mockRestore();
  });
});
