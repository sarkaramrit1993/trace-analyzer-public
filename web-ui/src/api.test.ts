import { describe, it, expect, vi, beforeEach } from 'vitest';
import { API_URL, HttpError, getJSON, postJSON } from './api';

const respond = (body: unknown, ok = true, status = 200) =>
  vi.fn(async () => ({ ok, status, json: async () => body }));

describe('api', () => {
  beforeEach(() => { vi.unstubAllGlobals(); });

  it('defaults API_URL to localhost:8080', () => {
    expect(API_URL).toBe('http://localhost:8080');
  });

  it('getJSON prefixes API_URL, passes the signal, and returns parsed JSON', async () => {
    const fetchMock = respond([{ id: 1 }]);
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    await expect(getJSON('/api/stats', controller.signal)).resolves.toEqual([{ id: 1 }]);
    expect(fetchMock).toHaveBeenCalledWith('http://localhost:8080/api/stats', { signal: controller.signal });
  });

  it('getJSON throws on a non-ok response', async () => {
    vi.stubGlobal('fetch', respond({}, false, 503));
    const err = await getJSON('/api/stats').catch(e => e);
    expect(err).toBeInstanceOf(HttpError);
    expect(err).toMatchObject({ status: 503, message: 'GET /api/stats failed: 503' });
  });

  it('getJSON throws if the signal aborts before the body is parsed', async () => {
    const controller = new AbortController();
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => { controller.abort(); return {}; } })));
    await expect(getJSON('/api/stats', controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('postJSON sends a JSON body and returns parsed JSON', async () => {
    const fetchMock = respond({ combination: 'ewma:50' });
    vi.stubGlobal('fetch', fetchMock);
    await expect(postJSON('/api/variants/combination', { combination: 'ewma:50' })).resolves.toEqual({ combination: 'ewma:50' });
    expect(fetchMock).toHaveBeenCalledWith('http://localhost:8080/api/variants/combination', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ combination: 'ewma:50' }),
    });
  });

  it('postJSON throws on a non-ok response', async () => {
    vi.stubGlobal('fetch', respond({}, false, 400));
    await expect(postJSON('/api/variants/combination', {})).rejects.toThrow('POST /api/variants/combination failed: 400');
  });
});
