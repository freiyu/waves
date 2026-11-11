// A transport turns one request into one response. The HTTP transport talks to
// the Core Service; the mock transport (src/mock) answers from memory. The
// typed client sits on top of either, so both paths run the same client code.

export type Method = 'get' | 'post' | 'patch' | 'put' | 'delete';

export interface TransportRequest {
  method: Method;
  /** Path under /api/v1 with parameters filled in, plus the query string. */
  url: string;
  headers: Record<string, string>;
  body?: string;
}

export interface TransportResponse {
  status: number;
  text: string;
}

export type Transport = (req: TransportRequest) => Promise<TransportResponse>;

export const API_BASE = '/api/v1';

export const httpTransport: Transport = async (req) => {
  const res = await fetch(API_BASE + req.url, {
    method: req.method.toUpperCase(),
    headers: req.headers,
    body: req.body,
    credentials: 'same-origin',
    cache: 'no-store',
  });
  return { status: res.status, text: await res.text() };
};
