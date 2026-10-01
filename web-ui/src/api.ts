declare global {
  interface Window {
    APP_CONFIG?: {
      API_URL?: string;
      ANOMALY_BASELINE_PERCENTILE?: number;
    };
  }
}

export const API_URL = window.APP_CONFIG?.API_URL || import.meta.env.VITE_API_URL || 'http://localhost:8080';

export class HttpError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

// fetch implementations (and test mocks) may ignore the signal once the body is read,
// so check again before handing data to the caller.
async function readJSON<T>(res: Response, label: string, signal?: AbortSignal): Promise<T> {
  if (!res.ok) throw new HttpError(res.status, `${label} failed: ${res.status}`);
  const data = (await res.json()) as T;
  signal?.throwIfAborted();
  return data;
}

export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, { signal });
  return readJSON<T>(res, `GET ${path}`, signal);
}

export async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return readJSON<T>(res, `POST ${path}`);
}
