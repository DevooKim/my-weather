// 앱 상태 (Svelte 5 runes). 장소·타임라인·즐겨찾기·관리자 상태.
import { api, ApiError, favToPlace, type FavList } from './api';
import { placeKey, samePlace, store } from './storage';
import type { NowItem, Place, Timeline } from './types';

export type LoadError =
	| { kind: 'unavailable'; message: string; retryAt: number }
	| { kind: 'network'; message: string }
	| { kind: 'other'; message: string };

class AppState {
	place = $state<Place | null>(null);
	timeline = $state<Timeline | null>(null);
	/** 오프라인이라 저장해 둔 타임라인을 보여 줄 때, 받은 시각(ms) */
	staleAt = $state<number | null>(null);
	loading = $state(false);
	error = $state<LoadError | null>(null);

	favs = $state<Place[]>([]);
	favVersion = $state<number | null>(null);
	recent = $state<Place[]>([]);
	now = $state<Record<string, NowItem>>({});

	admin = $state(false);
	authKnown = $state(false);

	sheetOpen = $state(false);
	/** 시트를 닫을 수 없음(장소가 하나도 없을 때) */
	sheetForced = $state(false);
	loginOpen = $state(false);
	installOpen = $state(false);
	settingsOpen = $state(false);
	toast = $state<string | null>(null);

	#timer: ReturnType<typeof setTimeout> | null = null;
	#toastTimer: ReturnType<typeof setTimeout> | null = null;
	#seq = 0;
	#lastFetch = 0;

	init() {
		this.favs = store.favs();
		this.recent = store.recent();
		const last = store.last();
		const first = placeFromUrl() ?? last ?? this.favs[0] ?? null;
		if (first) this.select(first, { remember: false });
		else {
			this.sheetOpen = true;
			this.sheetForced = true;
		}
		this.refreshNow();
		this.checkAuth();
		document.addEventListener('visibilitychange', () => {
			if (document.visibilityState === 'visible' && Date.now() - this.#lastFetch > 60_000) this.reload();
		});
		setInterval(() => {
			if (document.visibilityState === 'visible' && Date.now() - this.#lastFetch > 5 * 60_000) this.reload();
		}, 60_000);
	}

	showToast(msg: string) {
		this.toast = msg;
		if (this.#toastTimer) clearTimeout(this.#toastTimer);
		this.#toastTimer = setTimeout(() => (this.toast = null), 3500);
	}

	// ── 장소·타임라인 ─────────────────────────────

	select(p: Place, opt: { remember?: boolean } = {}) {
		const changed = !samePlace(this.place, p);
		this.place = p;
		this.sheetForced = false;
		store.setLast(p);
		if (opt.remember !== false && !this.isFav(p)) {
			this.recent = [p, ...this.recent.filter((r) => !samePlace(r, p))].slice(0, 6);
			store.setRecent(this.recent);
		}
		if (changed) {
			this.timeline = null;
			this.error = null;
			const c = store.cachedTimeline(p);
			// 저장본은 바로 보여 주되, 오래됐으면 받는 동안만
			if (c && Date.now() - c.at < 6 * 3600_000) {
				this.timeline = c.tl;
				this.staleAt = c.at;
			}
		}
		this.reload();
	}

	reload() {
		const p = this.place;
		if (!p) return;
		if (this.#timer) clearTimeout(this.#timer);
		this.#timer = null;
		const seq = ++this.#seq;
		this.loading = true;
		this.#lastFetch = Date.now();
		api
			.timeline(p)
			.then((tl) => {
				if (seq !== this.#seq) return;
				this.timeline = tl;
				this.staleAt = null;
				this.error = null;
				// 표에 있는 이름·행정구역을 장소에 채운다
				if ((tl.place.full && !p.full) || !p.name) {
					const filled: Place = {
						...p,
						full: p.full || tl.place.full,
						region: p.region || tl.place.region,
						name: p.name || tl.place.name || `격자 ${p.nx},${p.ny}`
					};
					this.place = filled;
					store.setLast(filled);
					this.recent = this.recent.map((r) => (samePlace(r, p) ? filled : r));
					store.setRecent(this.recent);
				}
				if (tl.complete) store.cacheTimeline(p, tl);
				else {
					const s = Math.min(Math.max(tl.retry_after_s ?? 5, 2), 60);
					this.#timer = setTimeout(() => this.reload(), s * 1000);
				}
			})
			.catch((e: unknown) => {
				if (seq !== this.#seq) return;
				const err = e instanceof ApiError ? e : new ApiError(0, 'network', String(e));
				if (err.network) {
					const c = store.cachedTimeline(p);
					if (c && !this.timeline) this.timeline = c.tl;
					if (this.timeline) this.staleAt = this.staleAt ?? c?.at ?? Date.now();
					this.error = { kind: 'network', message: err.message };
					this.#timer = setTimeout(() => this.reload(), 60_000);
				} else if (err.status === 503) {
					const s = err.retryAfter && err.retryAfter > 0 ? Math.min(err.retryAfter, 120) : 60;
					// 화면 문구는 "1분 뒤 자동으로 다시 시도"이므로 짧은 Retry-After도 그대로 따른다
					this.error = { kind: 'unavailable', message: err.message, retryAt: Date.now() + s * 1000 };
					this.#timer = setTimeout(() => this.reload(), s * 1000);
				} else if (err.status === 429) {
					const s = err.retryAfter ?? 30;
					this.error = { kind: 'other', message: err.message };
					this.#timer = setTimeout(() => this.reload(), s * 1000);
				} else {
					this.error = { kind: 'other', message: err.message };
				}
			})
			.finally(() => {
				if (seq === this.#seq) this.loading = false;
			});
	}

	// ── 즐겨찾기 ───────────────────────────────

	isFav(p: Place) {
		return this.favs.some((f) => samePlace(f, p));
	}
	favOf(p: Place) {
		return this.favs.find((f) => samePlace(f, p));
	}

	#setFavs(l: Place[]) {
		this.favs = l;
		store.setFavs(l);
		this.recent = this.recent.filter((r) => !this.isFav(r));
		store.setRecent(this.recent);
	}

	#applyServer(list: FavList) {
		// 서버에는 full이 없으므로 아는 값으로 채운다
		const known = [...this.favs, ...this.recent, ...(this.place ? [this.place] : [])];
		const items = [...list.items].sort((a, b) => a.pos - b.pos).map((f) => {
			const k = known.find((x) => placeKey(x) === placeKey(f));
			return favToPlace(f, k?.full);
		});
		this.favVersion = list.version;
		this.#setFavs(items);
		const now: Record<string, NowItem> = { ...this.now };
		for (const f of list.items) if (f.now) now[`${f.nx},${f.ny}`] = { nx: f.nx, ny: f.ny, ...f.now } as NowItem;
		this.now = now;
		// 지금 보는 장소에 id·alerts 반영
		if (this.place) {
			const cur = items.find((x) => samePlace(x, this.place));
			if (cur) this.place = { ...this.place, id: cur.id, alerts: cur.alerts };
		}
	}

	async toggleStar(p: Place) {
		const existing = this.favOf(p);
		if (!this.admin) {
			if (existing) this.#setFavs(this.favs.filter((f) => !samePlace(f, p)));
			else this.#setFavs([...this.favs, { ...p, id: undefined, alerts: undefined }]);
			this.refreshNow();
			return;
		}
		try {
			if (existing?.id != null) {
				const r = await api.deleteFavorite(existing.id);
				this.favVersion = r.version;
				this.#setFavs(this.favs.filter((f) => !samePlace(f, p)));
			} else {
				const r = await api.addFavorite({ name: p.name, region: p.region, nx: p.nx, ny: p.ny });
				this.favVersion = r.version;
				this.#setFavs([...this.favs.filter((f) => !samePlace(f, p)), favToPlace(r.item, p.full)]);
			}
		} catch (e) {
			this.#serverFail(e);
		}
	}

	async removeFav(p: Place) {
		if (this.isFav(p)) await this.toggleStar(p);
	}

	async reorder(next: Place[]) {
		const prev = this.favs;
		this.#setFavs(next);
		if (!this.admin) return;
		const ids = next.map((f) => f.id).filter((x): x is number => x != null);
		try {
			const r = await api.orderFavorites(ids, this.favVersion ?? 0);
			this.#applyServer(r);
		} catch (e) {
			if (e instanceof ApiError && e.status === 409 && e.body?.items) {
				this.#applyServer(e.body as FavList);
				this.showToast('다른 기기에서 목록이 바뀌었어요. 최신 목록을 보여 드려요.');
			} else {
				this.#setFavs(prev);
				this.#serverFail(e);
			}
		}
	}

	async rename(p: Place, name: string) {
		const f = this.favOf(p);
		if (!f) return;
		if (!this.admin || f.id == null) {
			this.#setFavs(this.favs.map((x) => (samePlace(x, p) ? { ...x, name } : x)));
			return;
		}
		try {
			const r = await api.renameFavorite(f.id, name);
			this.favVersion = r.version ?? this.favVersion;
			this.#setFavs(this.favs.map((x) => (x.id === f.id ? { ...x, name } : x)));
		} catch (e) {
			this.#serverFail(e);
		}
		if (this.place && samePlace(this.place, p)) this.place = { ...this.place, name };
	}

	async setAlerts(p: Place, alerts: { soon?: boolean; eve?: boolean }) {
		const f = this.favOf(p);
		if (!f?.id) return;
		try {
			const r = await api.patchAlerts([{ id: f.id, ...alerts }]);
			const map = new Map(r.favorites.map((x) => [x.id, x]));
			this.#setFavs(
				this.favs.map((x) => {
					const m = x.id != null ? map.get(x.id) : undefined;
					return m ? { ...x, alerts: { soon: m.soon, eve: m.eve } } : x;
				})
			);
			return r;
		} catch (e) {
			this.#serverFail(e);
		}
	}

	#serverFail(e: unknown) {
		if (e instanceof ApiError && e.status === 401) {
			this.admin = false;
			this.showToast('로그인이 풀렸어요. 메뉴 버튼을 빠르게 세 번 눌러 다시 로그인하세요.');
			return;
		}
		this.showToast(e instanceof ApiError ? e.message : '요청이 실패했어요.');
	}

	async refreshNow() {
		const list = this.favs.slice(0, 20);
		if (!list.length) return;
		try {
			const r = await api.now(list);
			const now: Record<string, NowItem> = { ...this.now };
			for (const it of r.items) now[`${it.nx},${it.ny}`] = it;
			this.now = now;
		} catch {
			/* /api/now가 없거나 실패하면 기온을 비워 둔다 */
		}
	}

	nowFor(p: Place): NowItem | undefined {
		// 지금 보는 장소는 타임라인 값이 가장 정확하다
		if (this.timeline && this.place && samePlace(p, this.place) && this.timeline.summary.now) {
			const n = this.timeline.summary.now;
			return { nx: p.nx, ny: p.ny, tmp: n.tmp, sky: n.sky, pty: n.wet ? n.pty : 'none' };
		}
		return this.now[`${p.nx},${p.ny}`];
	}

	// ── 관리자 ────────────────────────────────

	async checkAuth() {
		try {
			const me = await api.me();
			this.admin = !!me.admin;
		} catch {
			this.admin = false; // 404(아직 없음)·오프라인 모두 비로그인으로 본다
		}
		this.authKnown = true;
		if (this.admin) await this.syncServerFavs();
		this.openFromUrl();
	}

	/** 알림을 눌러 열었을 때 /?fav=1 */
	openFromUrl() {
		const q = new URLSearchParams(location.search);
		const fav = Number(q.get('fav'));
		if (!fav) return;
		const f = this.favs.find((x) => x.id === fav);
		if (f) this.select(f, { remember: false });
		history.replaceState(history.state, '', location.pathname);
	}

	async syncServerFavs() {
		try {
			this.#applyServer(await api.favorites());
		} catch (e) {
			this.#serverFail(e);
		}
	}

	/** 로그인 직후: 이 브라우저의 별 목록을 서버와 합친다 (§2.5 merge) */
	async afterLogin() {
		this.admin = true;
		this.loginOpen = false;
		try {
			const local = this.favs.map((f) => ({ name: f.name, region: f.region, nx: f.nx, ny: f.ny }));
			const r = await api.mergeFavorites(local);
			this.#applyServer(r);
			this.showToast(
				r.added > 0 ? `로그인했어요. 별 ${r.added}곳을 서버 즐겨찾기에 합쳤어요.` : '로그인했어요.'
			);
		} catch (e) {
			this.#serverFail(e);
		}
	}

	async logout() {
		try {
			await api.logout();
		} catch {
			/* 쿠키가 이미 없어도 로그아웃으로 본다 */
		}
		this.admin = false;
		// 합친 별 목록은 그 브라우저에 그대로 둔다(§2). 서버 id·알림 표시만 지운다.
		this.#setFavs(this.favs.map((f) => ({ name: f.name, full: f.full, region: f.region, nx: f.nx, ny: f.ny })));
		if (this.place) this.place = { ...this.place, id: undefined, alerts: undefined };
		this.favVersion = null;
		this.showToast('로그아웃했어요. 별 목록은 이 브라우저에 남아 있어요.');
	}
}

/** 공유 링크 /?nx=59&ny=126&region=1144066000 (&name=) */
function placeFromUrl(): Place | null {
	const q = new URLSearchParams(location.search);
	const nx = Number(q.get('nx')),
		ny = Number(q.get('ny'));
	if (!(nx >= 1 && nx <= 149 && ny >= 1 && ny <= 253)) return null;
	const region = q.get('region') || undefined;
	const name = q.get('name') || '';
	history.replaceState(history.state, '', location.pathname);
	return { name, region, nx, ny };
}

export const app = new AppState();
