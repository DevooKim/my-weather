// localStorage 래퍼. 사파리 비공개 모드 등에서 실패해도 앱은 돈다.
import type { Place, Timeline } from './types';

function read<T>(key: string, fallback: T): T {
	try {
		const v = localStorage.getItem(key);
		return v ? (JSON.parse(v) as T) : fallback;
	} catch {
		return fallback;
	}
}
function write(key: string, v: unknown) {
	try {
		if (v === null || v === undefined) localStorage.removeItem(key);
		else localStorage.setItem(key, JSON.stringify(v));
	} catch {
		/* 용량 초과·차단 */
	}
}

export const K = {
	favs: 'mw.favs',
	recent: 'mw.recent',
	last: 'mw.last',
	tlKeys: 'mw.tl.keys'
};

export function placeKey(p: { region?: string; nx: number; ny: number; name?: string }) {
	return p.region ? `r:${p.region}` : `g:${p.nx},${p.ny},${p.name ?? ''}`;
}

export function samePlace(a: Place | null | undefined, b: Place | null | undefined) {
	return !!a && !!b && placeKey(a) === placeKey(b);
}

const clean = (p: Place): Place => ({
	name: p.name,
	full: p.full,
	region: p.region,
	nx: p.nx,
	ny: p.ny,
	...(p.id != null ? { id: p.id } : {}),
	...(p.alerts ? { alerts: p.alerts } : {})
});

export const store = {
	favs: () => read<Place[]>(K.favs, []),
	setFavs: (l: Place[]) => write(K.favs, l.map(clean)),
	recent: () => read<Place[]>(K.recent, []),
	setRecent: (l: Place[]) => write(K.recent, l.slice(0, 6).map(clean)),
	last: () => read<Place | null>(K.last, null),
	setLast: (p: Place | null) => write(K.last, p ? clean(p) : null),

	/** 마지막으로 받은 타임라인 (오프라인 표시용, 최근 3곳) */
	cachedTimeline(p: Place): { at: number; tl: Timeline } | null {
		return read(`mw.tl.${placeKey(p)}`, null);
	},
	cacheTimeline(p: Place, tl: Timeline) {
		const key = `mw.tl.${placeKey(p)}`;
		const keys = read<string[]>(K.tlKeys, []).filter((k) => k !== key);
		keys.unshift(key);
		for (const old of keys.splice(3)) write(old, null);
		write(K.tlKeys, keys);
		write(key, { at: Date.now(), tl });
	}
};
