// HTTP API 호출. 계약: docs/api-design-v1.md §2
import type { NowItem, Place, Region, Timeline } from './types';

export class ApiError extends Error {
	constructor(
		public status: number,
		public code: string,
		message: string,
		public retryAfter: number | null = null,
		public body: any = null
	) {
		super(message);
	}
	/** 서버에 닿지 못함(오프라인, DNS, 타임아웃) */
	get network() {
		return this.status === 0;
	}
}

async function call<T>(method: string, path: string, body?: unknown, timeoutMs = 15000): Promise<T> {
	const ctl = new AbortController();
	const timer = setTimeout(() => ctl.abort(), timeoutMs);
	let res: Response;
	try {
		res = await fetch(path, {
			method,
			credentials: 'same-origin',
			headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
			body: body !== undefined ? JSON.stringify(body) : undefined,
			signal: ctl.signal
		});
	} catch {
		throw new ApiError(0, 'network', '서버에 연결할 수 없어요.');
	} finally {
		clearTimeout(timer);
	}
	if (res.status === 204) return undefined as T;
	let data: any = null;
	const text = await res.text();
	try {
		data = text ? JSON.parse(text) : null;
	} catch {
		data = null;
	}
	if (!res.ok) {
		const ra = Number(res.headers.get('Retry-After') ?? data?.error?.retry_after_s ?? NaN);
		throw new ApiError(
			res.status,
			data?.error?.code ?? 'http_' + res.status,
			data?.error?.message ?? `요청이 실패했어요 (${res.status})`,
			Number.isFinite(ra) ? ra : null,
			data
		);
	}
	return data as T;
}

const get = <T>(p: string) => call<T>('GET', p);

export function placeQuery(p: { nx: number; ny: number; region?: string }) {
	const q = new URLSearchParams({ nx: String(p.nx), ny: String(p.ny) });
	if (p.region) q.set('region', p.region);
	return q.toString();
}

export const api = {
	timeline: (p: { nx: number; ny: number; region?: string }) =>
		call<Timeline>('GET', '/api/timeline?' + placeQuery(p), undefined, 20000),
	search: (q: string, limit = 20) =>
		get<{ items: Region[] }>(`/api/search?q=${encodeURIComponent(q)}&limit=${limit}`),
	grid: (lat: number, lon: number) =>
		get<{
			nx: number | null;
			ny: number | null;
			outside: boolean;
			region: { code: string; name: string; full: string; distance_km: number } | null;
		}>(`/api/grid?lat=${lat.toFixed(4)}&lon=${lon.toFixed(4)}`),
	now: (places: { nx: number; ny: number }[]) =>
		get<{ items: NowItem[] }>(
			'/api/now?' + new URLSearchParams({ places: places.map((p) => `${p.nx},${p.ny}`).join(';') })
		),

	// 인증 §2.8
	me: () => get<{ admin: boolean; role?: 'admin' | 'member'; name?: string; passkeys?: number }>('/api/auth/me'),
	issueEnroll: (role: 'admin' | 'member') =>
		call<{ token: string; role: string; expires_at: string }>('POST', '/api/auth/enroll-tokens', { role }),
	loginOptions: () => call<{ publicKey: any }>('POST', '/api/auth/login/options', {}),
	loginVerify: (credential: unknown) =>
		call<{ ok: boolean; expires_at?: string }>('POST', '/api/auth/login/verify', { credential }),
	registerOptions: (token: string, label: string) =>
		call<{ publicKey: any }>('POST', '/api/auth/register/options', { token, label }),
	registerVerify: (token: string, credential: unknown) =>
		call<{ ok: boolean; passkey?: { id: string; label: string; created_at: string } }>(
			'POST',
			'/api/auth/register/verify',
			{ token, credential }
		),
	logout: () => call<void>('POST', '/api/auth/logout', {}),
	passkeys: () => get<any>('/api/auth/passkeys'),

	// 즐겨찾기 §2.5
	favorites: () => get<FavList>('/api/favorites'),
	addFavorite: (p: { name: string; region?: string; nx: number; ny: number }) =>
		call<{ item: ServerFav; version: number }>('POST', '/api/favorites', p),
	deleteFavorite: (id: number) =>
		call<{ deleted: number; version: number }>('DELETE', `/api/favorites/${id}`),
	renameFavorite: (id: number, name: string) =>
		call<{ item: ServerFav; version: number }>('PATCH', `/api/favorites/${id}`, { name }),
	orderFavorites: (ids: number[], version: number) =>
		call<FavList>('PUT', '/api/favorites/order', { ids, version }),
	mergeFavorites: (items: { name: string; region?: string; nx: number; ny: number }[]) =>
		call<FavList & { added: number; existing: number }>('POST', '/api/favorites/merge', { items }),

	// 알림 §2.6
	alertSettings: () => get<AlertSettings>('/api/alerts/settings'),
	patchAlerts: (favorites: { id: number; soon?: boolean; eve?: boolean }[]) =>
		call<AlertSettings>('PATCH', '/api/alerts/settings', { favorites }),

	// 푸시 §2.7
	vapidKey: () => get<{ key: string }>('/api/push/vapid-public-key'),
	subscribe: (sub: PushSubscriptionJSON, label: string) =>
		call<{ id: number }>('POST', '/api/push/subscriptions', { ...sub, label }),
	unsubscribe: (endpoint: string) => call<void>('DELETE', '/api/push/subscriptions', { endpoint }),
	pushTest: (endpoint?: string) =>
		call<{ sent: number; failed: number; removed: number }>(
			'POST',
			'/api/push/test',
			endpoint ? { endpoint } : {}
		),

	// 설정 화면 정보 (backend 3단계에서 추가. 형식이 정해지기 전이라 느슨하게 읽는다)
	settingsInfo: () => get<any>('/api/settings/info')
};

export interface ServerFav {
	id: number;
	name: string;
	region?: string;
	nx: number;
	ny: number;
	pos: number;
	alerts?: { soon: boolean; eve: boolean };
	now?: { tmp: number | null; sky?: string | null; pty?: string | null; observed_at?: string } | null;
}
export interface FavList {
	items: ServerFav[];
	version: number;
}
export interface AlertSettings {
	favorites: { id: number; name: string; soon: boolean; eve: boolean }[];
	rules?: Record<string, unknown>;
	push_devices?: number;
}

export function favToPlace(f: ServerFav, full?: string): Place {
	return {
		id: f.id,
		name: f.name,
		region: f.region || undefined,
		nx: f.nx,
		ny: f.ny,
		full,
		alerts: f.alerts ?? { soon: false, eve: false }
	};
}
