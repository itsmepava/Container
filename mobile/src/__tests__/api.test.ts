import { api, ApiError, setToken, setUnauthorizedHandler } from '@/lib/api';

describe('api client', () => {
  afterEach(() => {
    setToken(null);
    setUnauthorizedHandler(null);
  });

  it('sends the bearer token and parses JSON', async () => {
    const fetchMock = jest.fn().mockResolvedValue({ ok: true, status: 200, json: async () => [{ id: 1 }] });
    (globalThis as any).fetch = fetchMock;
    setToken('abc');
    const r = await api.tenders('open');
    expect(r).toEqual([{ id: 1 }]);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toMatch(/\/api\/v1\/tenders\?scope=open$/);
    expect(init.headers.Authorization).toBe('Bearer abc');
  });

  it('surfaces server messages and signs out on 401', async () => {
    (globalThis as any).fetch = jest.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ error: 'Please sign in again.' }) });
    const onOut = jest.fn();
    setToken('abc');
    setUnauthorizedHandler(onOut);
    await expect(api.me()).rejects.toEqual(new ApiError(401, 'Please sign in again.'));
    expect(onOut).toHaveBeenCalled();
  });

  it('explains network failures', async () => {
    (globalThis as any).fetch = jest.fn().mockRejectedValue(new TypeError('Network request failed'));
    await expect(api.meta()).rejects.toThrow("Can't reach Eimsky");
  });
});
