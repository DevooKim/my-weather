<script lang="ts">
	import { api, ApiError } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import { deg, district, shortFull, weatherWord } from '$lib/format';
	import { samePlace } from '$lib/storage';
	import type { Place, Region } from '$lib/types';
	import Icon from './Icon.svelte';
	import FavRow from './FavRow.svelte';

	let {
		onpick,
		onbell,
		variant = 'sheet'
	}: { onpick: (p: Place) => void; onbell: (p: Place) => void; variant?: 'sheet' | 'aside' } = $props();

	let q = $state('');
	let results = $state<Region[] | null>(null);
	let searching = $state(false);
	let searchErr = $state('');
	let input: HTMLInputElement | undefined = $state();
	let geo = $state<'idle' | 'busy' | 'denied' | 'outside' | 'error' | 'unsupported'>('idle');

	let timer: ReturnType<typeof setTimeout> | null = null;
	let seq = 0;
	$effect(() => {
		const term = q.trim();
		if (timer) clearTimeout(timer);
		searchErr = '';
		if (!term) {
			results = null;
			return;
		}
		timer = setTimeout(async () => {
			const my = ++seq;
			searching = true;
			try {
				const r = await api.search(term);
				if (my === seq) results = r.items;
			} catch (e) {
				if (my === seq) searchErr = e instanceof ApiError ? e.message : '검색하지 못했어요.';
			} finally {
				if (my === seq) searching = false;
			}
		}, 250);
	});

	const regionPlace = (r: Region): Place => ({ name: r.name, full: r.full, region: r.code, nx: r.nx, ny: r.ny });

	function locate() {
		if (!('geolocation' in navigator)) {
			geo = 'unsupported';
			return;
		}
		geo = 'busy';
		navigator.geolocation.getCurrentPosition(
			async (pos) => {
				try {
					const g = await api.grid(pos.coords.latitude, pos.coords.longitude);
					if (g.outside || g.nx == null || g.ny == null) {
						geo = 'outside';
						return;
					}
					geo = 'idle';
					onpick(
						g.region
							? { name: g.region.name, full: g.region.full, region: g.region.code, nx: g.nx, ny: g.ny }
							: { name: '현재 위치', nx: g.nx, ny: g.ny }
					);
				} catch {
					geo = 'error';
				}
			},
			(err) => {
				geo = err.code === err.PERMISSION_DENIED ? 'denied' : 'error';
			},
			{ enableHighAccuracy: false, timeout: 10000, maximumAge: 5 * 60_000 }
		);
	}

	function hl(name: string, term: string) {
		const t = term.replace(/\s+/g, '');
		const i = t ? name.indexOf(t) : -1;
		if (i < 0) return [{ t: name, b: false }];
		return [
			{ t: name.slice(0, i), b: false },
			{ t: name.slice(i, i + t.length), b: true },
			{ t: name.slice(i + t.length), b: false }
		];
	}

	const nowText = (p: Place) => {
		const n = app.nowFor(p);
		if (!n || n.tmp == null) return '';
		const w = weatherWord({ sky: n.sky ?? null, pty: n.pty ?? null, wet: !!n.pty && n.pty !== 'none' });
		return `${deg(n.tmp)}${w ? ' ' + w : ''}`;
	};

	// 끌어서 순서 바꾸기
	let dragFrom = $state<number | null>(null);
	let dragTo = $state<number | null>(null);
	function onDrag(from: number, to: number | null, done: boolean) {
		if (done) {
			if (to != null && to !== from) {
				const next = [...app.favs];
				const [m] = next.splice(from, 1);
				next.splice(to, 0, m);
				app.reorder(next);
			}
			dragFrom = dragTo = null;
		} else {
			dragFrom = from;
			dragTo = to;
		}
	}
	const shift = (i: number) => {
		if (dragFrom == null || dragTo == null || i === dragFrom) return 0;
		if (dragFrom < dragTo && i > dragFrom && i <= dragTo) return -1;
		if (dragFrom > dragTo && i < dragFrom && i >= dragTo) return 1;
		return 0;
	};

	export function focusSearch() {
		input?.focus();
	}
</script>

<div class="list {variant}">
	{#if app.admin && variant === 'sheet'}
		<div class="badge-row"><span class="badge">관리자</span></div>
	{/if}
	<label class="search" class:active={q.length > 0}>
		<Icon name="search" size={18} color="var(--text-3)" />
		<input
			bind:this={input}
			type="search"
			bind:value={q}
			placeholder={variant === 'aside' ? '동 이름으로 검색' : '동 이름으로 검색 (예: 서교동)'}
			aria-label="지역 검색"
			enterkeyhint="search"
			autocomplete="off"
			autocapitalize="off"
			spellcheck="false"
			onkeydown={(e) => {
				if (e.key === 'Enter' && results?.length) onpick(regionPlace(results[0]));
			}}
		/>
	</label>

	{#if q.trim()}
		<div class="results" aria-live="polite">
			{#if searchErr}
				<p class="empty">{searchErr}</p>
			{:else if results && results.length === 0 && !searching}
				<p class="empty">'{q.trim()}'와 맞는 동이 없어요. 동 이름으로 검색해 보세요. 예: 서교동, 역삼1동</p>
			{:else if results}
				{#each results as r (r.code)}
					{@const p = regionPlace(r)}
					<div class="row line">
						<button type="button" class="name" onclick={() => onpick(p)}>
							<span class="dim">{district(r.full)}&nbsp;</span>{#each hl(r.name, q) as part, k (k)}{#if part.b}<b>{part.t}</b>{:else}{part.t}{/if}{/each}
						</button>
						<button
							type="button"
							class="icon-btn"
							aria-label={app.isFav(p) ? `${r.name} 즐겨찾기에서 빼기` : `${r.name} 즐겨찾기에 추가`}
							aria-pressed={app.isFav(p)}
							onclick={() => app.toggleStar(p)}
						>
							<Icon name={app.isFav(p) ? 'star-fill' : 'star'} size={22} color={app.isFav(p) ? 'var(--star)' : 'var(--text-3)'} />
						</button>
					</div>
				{/each}
			{:else}
				<p class="empty">찾는 중…</p>
			{/if}
		</div>
	{:else}
		<button type="button" class="locate" onclick={locate} disabled={geo === 'busy'}>
			<Icon name="locate" size={20} />
			{geo === 'busy' ? '위치 확인 중…' : variant === 'aside' ? '현재 위치 보기' : '현재 위치'}
		</button>
		{#if geo === 'denied' || geo === 'unsupported'}
			<div class="note">
				<span>위치 권한이 꺼져 있어요. iPhone 설정 → Safari → 위치에서 허용하거나, 동 이름으로 찾아보세요.</span>
				<button type="button" class="dark-pill" onclick={() => input?.focus()}>동 이름으로 찾기</button>
			</div>
		{:else if geo === 'outside'}
			<div class="note"><span>지금 위치는 기상청 예보 범위 밖이에요. 동 이름으로 찾아보세요.</span></div>
		{:else if geo === 'error'}
			<div class="note"><span>위치를 알아내지 못했어요. 잠시 뒤 다시 시도하거나 동 이름으로 찾아보세요.</span></div>
		{/if}

		{#if variant === 'aside' && app.place && !app.isFav(app.place)}
			{@const cur = app.place}
			<div class="cap">지금 보는 곳</div>
			<ul class="favs" role="list">
				<FavRow
					place={cur}
					index={0}
					count={1}
					current={true}
					nowText={nowText(cur)}
					admin={false}
					shift={0}
					dragging={false}
					card={true}
					starred={false}
					onpick={() => {}}
					onstar={() => app.toggleStar(cur)}
					onbell={() => {}}
					ondrag={() => {}}
				/>
			</ul>
		{/if}

		{#if app.favs.length}
			<div class="cap">즐겨찾기{app.favs.length > 1 ? ' · 끌어서 순서 변경' : ''}</div>
			<ul class="favs" role="list">
				{#each app.favs as f, i (f.region ?? `${f.nx},${f.ny},${f.name}`)}
					<FavRow
						place={f}
						index={i}
						count={app.favs.length}
						current={samePlace(f, app.place)}
						nowText={nowText(f)}
						admin={app.admin}
						shift={shift(i)}
						dragging={dragFrom === i}
						card={variant === 'aside'}
						onpick={() => onpick(f)}
						onstar={() => app.toggleStar(f)}
						onbell={() => onbell(f)}
						ondrag={onDrag}
					/>
				{/each}
			</ul>
		{/if}

		{#if app.recent.length}
			<div class="cap">최근 본 곳</div>
			{#each app.recent.slice(0, 5) as r (r.region ?? `${r.nx},${r.ny},${r.name}`)}
				<div class="row">
					<button type="button" class="name" onclick={() => onpick(r)}>{shortFull(r.full, r.name)}</button>
					<button
						type="button"
						class="icon-btn"
						aria-label="{r.name} 즐겨찾기에 추가"
						aria-pressed="false"
						onclick={() => app.toggleStar(r)}
					>
						<Icon name="star" size={22} color="var(--text-3)" />
					</button>
				</div>
			{/each}
		{/if}
	{/if}
</div>

<style>
	.list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.badge-row {
		display: flex;
		justify-content: flex-end;
	}
	.badge {
		font-size: 11px;
		color: var(--accent-text);
		border: 1px solid var(--accent-border);
		border-radius: 10px;
		padding: 1px 8px;
	}
	.search {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 44px;
		padding: 0 12px;
		border: 1px solid var(--border-strong);
		border-radius: 10px;
		background: var(--surface);
		flex: none;
	}
	.search:focus-within {
		border: 2px solid var(--accent);
		padding: 0 11px;
	}
	.search input {
		flex-grow: 1;
		min-width: 0;
		border: 0;
		outline: none;
		font: inherit;
		font-size: 16px;
		color: var(--text);
		background: none;
	}
	.search input::placeholder {
		color: var(--text-3);
	}
	.locate {
		display: flex;
		align-items: center;
		gap: 10px;
		min-height: 48px;
		padding: 0 4px;
		background: none;
		border: 0;
		font: inherit;
		font-size: 15px;
		color: var(--accent-text);
		text-align: left;
		cursor: pointer;
	}
	.cap {
		font-size: 12px;
		color: var(--text-3);
		padding: 6px 4px 0;
	}
	.favs {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.row {
		display: flex;
		align-items: center;
		min-height: 44px;
		padding: 0 4px;
	}
	.row.line {
		border-bottom: 1px solid var(--line);
		min-height: 48px;
	}
	.name {
		flex-grow: 1;
		min-height: 44px;
		padding: 0;
		background: none;
		border: 0;
		font: inherit;
		font-size: 15px;
		text-align: left;
		color: var(--text);
		cursor: pointer;
	}
	.dim {
		color: var(--text-2);
	}
	.icon-btn {
		width: 44px;
		height: 44px;
		flex: none;
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: 0;
		cursor: pointer;
	}
	.empty {
		margin: 4px 0;
		padding: 14px 16px;
		background: var(--surface-2);
		border-radius: 12px;
		font-size: 14px;
		color: var(--text-2);
	}
	.note {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding: 14px 16px;
		background: var(--surface-2);
		border-radius: 12px;
		font-size: 14px;
		color: var(--text-2);
	}
	.dark-pill {
		align-self: flex-start;
		min-height: 44px;
		padding: 0 16px;
		border: 0;
		border-radius: 22px;
		background: var(--btn-dark);
		color: var(--btn-dark-text);
		font: inherit;
		font-size: 13px;
	}
	.aside .search {
		font-size: 14px;
	}
</style>
