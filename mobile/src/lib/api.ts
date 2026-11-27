import Constants from 'expo-constants';
import { Platform } from 'react-native';

// API base URL: EXPO_PUBLIC_API_URL wins, then app.json extra.apiUrl.
export const API_URL: string = (
  process.env.EXPO_PUBLIC_API_URL ||
  (Constants.expoConfig?.extra as { apiUrl?: string } | undefined)?.apiUrl ||
  'http://localhost:8080'
).replace(/\/$/, '');

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

let token: string | null = null;
let onUnauthorized: (() => void) | null = null;

export function setToken(t: string | null) {
  token = t;
}
export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn;
}

async function request<T>(method: string, path: string, body?: unknown, form?: FormData): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let res: Response;
  try {
    res = await fetch(`${API_URL}/api/v1${path}`, {
      method,
      headers,
      body: form ?? (body !== undefined ? JSON.stringify(body) : undefined),
    });
  } catch {
    throw new ApiError(0, "Can't reach Eimsky. Check your connection and try again.");
  }
  let data: any = null;
  try {
    data = await res.json();
  } catch {}
  if (!res.ok) {
    if (res.status === 401 && token && onUnauthorized) onUnauthorized();
    throw new ApiError(res.status, data?.error || `Request failed (${res.status}).`);
  }
  return data as T;
}

// ---------- Types ----------

export type User = {
  id: number;
  name: string;
  phone: string;
  email: string;
  companyId: number;
  companyName: string;
  companyStatus: 'none' | 'pending' | 'approved' | 'rejected' | 'suspended';
};

export type Registration = {
  hasCompany: boolean;
  hasBR: boolean;
  hasLicence: boolean;
  hasAgent: boolean;
  lanes: number;
  submitted: boolean;
  status: string;
  note: string;
  readyToSubmit: boolean;
};

export type Me = {
  user: User;
  registration: Registration;
  company?: { id: number; name: string; regNumber: string; email: string; status: string; laneIds: number[] | null };
};

export type Vessel = {
  id: number;
  name: string;
  voyage: string;
  label: string;
  service: string;
  serviceText: string;
  etd: string;
  eta: string;
  transitDays: number;
  via: string;
};

export type Bid = {
  id: number;
  amount: number;
  vesselId: number;
  includes: Record<string, boolean>;
  note: string;
  submittedAt: string;
  updatedAt: string;
};

export type Tender = {
  id: number;
  ref: string;
  lane: string;
  origin: string;
  dest: string;
  week: number;
  boxCode: string;
  cbm: number;
  tonnes: number;
  lots: number;
  categories: string;
  closesAt: string;
  cfsCutoff: string;
  status: 'open' | 'closed' | 'awarded' | 'cancelled';
  open: boolean;
  vessel: Vessel | null;
  vessels?: Vessel[];
  myBid: Bid | null;
  result: '' | 'won' | 'lost';
  wonAmount?: number;
  originCfs: string;
};

export type Doc = { id: number; kind: string; label: string; filename: string; size: string; uploadedAt: string; deleted: boolean };

export type Shipper = {
  bookingRef: string;
  company: string;
  contactName: string;
  email: string;
  phone: string;
  cbm: number;
  kg: number;
  category: string;
  documents: Doc[];
};

export type Award = { tender: Tender; shippers: Shipper[]; originCfs: string; confirmation?: Doc };
export type Lane = { id: number; label: string; origin: string; dest: string };
export type Notice = { id: number; title: string; body: string; read: boolean; createdAt: string };
export type Inclusion = { key: string; label: string };

/** A picked file ready to upload. */
export type PickedFile = { uri: string; name: string; mimeType: string };

async function formWith(file: PickedFile, fields: Record<string, string> = {}): Promise<FormData> {
  const form = new FormData();
  for (const [k, v] of Object.entries(fields)) form.append(k, v);
  if (Platform.OS === 'web') {
    const blob = await (await fetch(file.uri)).blob();
    form.append('file', blob, file.name);
  } else {
    form.append('file', { uri: file.uri, name: file.name, type: file.mimeType } as any);
  }
  return form;
}

export const api = {
  meta: () => request<{ inclusions: Inclusion[]; devMode: boolean }>('GET', '/meta'),
  requestCode: (phone: string) => request<{ phone: string; devCode?: string }>('POST', '/auth/otp', { phone }),
  verify: (phone: string, code: string) => request<{ token: string; user: User; newUser: boolean }>('POST', '/auth/verify', { phone, code }),
  logout: () => request<{ ok: boolean }>('POST', '/auth/logout'),
  me: () => request<Me>('GET', '/me'),
  saveProfile: (p: { companyName: string; regNumber: string; contactName: string; email: string }) => request<{ user: User }>('PUT', '/me/profile', p),
  lanes: () => request<Lane[]>('GET', '/lanes'),
  setLanes: (laneIds: number[]) => request<{ ok: boolean }>('PUT', '/me/lanes', { laneIds }),
  myDocuments: () => request<Doc[]>('GET', '/me/documents'),
  uploadCompanyDoc: async (kind: string, file: PickedFile) => request<Doc>('POST', '/me/documents', undefined, await formWith(file, { kind })),
  submit: () => request<{ ok: boolean }>('POST', '/me/submit'),
  tenders: (scope: 'open' | 'mine' | 'awarded') => request<Tender[]>('GET', `/tenders?scope=${scope}`),
  tender: (id: number) => request<Tender>('GET', `/tenders/${id}`),
  bid: (id: number, b: { amount: number; vesselId: number; includes: Record<string, boolean>; note: string }) => request<Tender>('PUT', `/tenders/${id}/bid`, b),
  withdraw: (id: number) => request<Tender>('DELETE', `/tenders/${id}/bid`),
  award: (id: number) => request<Award>('GET', `/tenders/${id}/award`),
  uploadConfirmation: async (id: number, file: PickedFile) => request<Doc>('POST', `/tenders/${id}/confirmation`, undefined, await formWith(file)),
  documentLink: (id: number) => request<{ url: string }>('GET', `/documents/${id}/link`),
  savePushToken: (token: string, platform: string) => request<{ ok: boolean }>('POST', '/push-token', { token, platform }),
  notifications: () => request<{ items: Notice[]; unread: number }>('GET', '/notifications'),
  markRead: (id = 0) => request<{ ok: boolean }>('POST', '/notifications/read', { id }),
};
