<script lang="ts">
	// 1A(큰 현재 날씨) → 1C(접힌 크기) → 한 줄 머리. 높이 h는 부모가 스크롤로 정한다.
	import { app } from '$lib/app.svelte';
	import { deg, nowPrecipText, shortFull, summaryLines, weatherWord } from '$lib/format';
	import type { Scene } from '$lib/scene';
	import Icon from './Icon.svelte';
	import WeatherCanvas from './WeatherCanvas.svelte';

	let {
		h,
		full,
		compact,
		bar,
		scene,
		onplace,
		onmenu
	}: {
		h: number;
		full: number;
		compact: number;
		bar: number;
		scene: Scene;
		onplace: () => void;
		onmenu: () => void;
	} = $props();

	const tl = $derived(app.timeline);
	const nowSlot = $derived(tl?.slots.find((s) => s.phase === 'now') ?? null);
	const tmp = $derived(tl?.summary.now?.tmp ?? nowSlot?.tmp ?? null);
	const word = $derived.by(() => {
		if (!nowSlot) return '';
		const sky = nowSlot.sky ?? tl?.slots.find((s) => s.phase === 'future' && s.sky)?.sky ?? null;
		return weatherWord({ ...nowSlot, sky });
	});
	const precip = $derived(nowSlot ? nowPrecipText(nowSlot) : '');
	const lines = $derived(summaryLines(tl?.summary.text));
	const feels = $derived(
		nowSlot?.feels != null && tmp != null && Math.round(nowSlot.feels) !== Math.round(tmp) ? `체감 ${deg(nowSlot.feels)}` : ''
	);
	const desc1A = $derived(
		[precip ? precip.replace(/^(\S+) /, '$1 · 시간당 ') : word, feels].filter(Boolean).join(' · ')
	);
	const desc1C = $derived(precip || word);

	const placeLabel = $derived.by(() => {
		const p = app.place;
		if (!p) return '장소 고르기';
		const sf = shortFull(p.full, '');
		if (!sf) return p.name || `격자 ${p.nx},${p.ny}`;
		return sf.endsWith(p.name) ? sf : `${p.name} · ${sf}`;
	});

	// 0 = 1A, 1 = 1C
	const p = $derived(Math.min(1, Math.max(0, (full - h) / Math.max(1, full - compact))));
	const isBar = $derived(h <= bar + 24);
	const tempSize = $derived(Math.round(96 - 48 * p));
</script>

<header class="hero" style="height:{h}px">
	<WeatherCanvas {scene} height={full} />
	<div class="inner" style="height:{h}px">
		{#if isBar}
			<div class="barrow">
				<button type="button" class="place one" onclick={onplace} aria-label="장소 바꾸기. 지금 {placeLabel}">
					<span class="ell">{app.place?.name ?? '장소 고르기'}{tmp != null ? ` · ${deg(tmp)} ${desc1C}` : ''}</span>
				</button>
				<button type="button" class="menu" aria-label="메뉴" onclick={onmenu}><Icon name="menu" size={22} /></button>
			</div>
		{:else}
			<div class="top">
				<button type="button" class="place" onclick={onplace} aria-haspopup="dialog">
					<span class="ell">{placeLabel}</span><Icon name="chevron-down" size={16} />
				</button>
				<button type="button" class="menu" aria-label="메뉴" onclick={onmenu}><Icon name="menu" size={22} /></button>
			</div>
			{#if tl}
				<div class="big" class:row={p > 0.6} style="opacity:{Math.min(1, Math.max(0, (h - bar - 24) / 40))}">
					<span class="temp" style="font-size:{tempSize}px">{deg(tmp)}</span>
					<span class="desc">{p > 0.6 ? desc1C : desc1A}</span>
				</div>
				{#if lines.length}
					<div class="sum" class:one={p > 0.6}>
						{#if p > 0.6}
							<p>{lines.join(' · ')}</p>
						{:else}
							{#each lines as l, i (i)}<p>{l}</p>{/each}
						{/if}
					</div>
				{/if}
			{:else if app.error?.kind === 'unavailable' || app.error}
				<div class="big"><span class="desc">날씨를 불러오지 못했어요</span></div>
			{:else if app.place}
				<div class="big"><span class="desc">불러오는 중…</span></div>
			{:else}
				<div class="big"><span class="desc">장소를 골라 주세요</span></div>
			{/if}
		{/if}
	</div>
</header>

<style>
	.hero {
		position: fixed;
		top: 0;
		left: 0;
		right: 0;
		z-index: 10;
		overflow: hidden;
		color: #fff;
		background: #2b3440;
	}
	.inner {
		position: relative;
		max-width: 720px;
		margin: 0 auto;
		padding: calc(8px + env(safe-area-inset-top)) 12px 16px 20px;
		display: flex;
		flex-direction: column;
	}
	.top,
	.barrow {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
	}
	.barrow {
		margin-top: -4px;
	}
	.place {
		display: flex;
		align-items: center;
		gap: 4px;
		min-height: 44px;
		min-width: 0;
		padding: 0;
		background: none;
		border: 0;
		color: #fff;
		font: inherit;
		font-size: 16px;
		font-weight: 500;
		text-align: left;
	}
	.place.one {
		font-size: 15px;
		flex-grow: 1;
	}
	.ell {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.menu {
		width: 44px;
		height: 44px;
		flex: none;
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: 0;
		color: #fff;
	}
	.big {
		display: flex;
		flex-direction: column;
		justify-content: center;
		gap: 4px;
		flex-grow: 1;
		min-height: 0;
	}
	.big.row {
		flex-direction: row;
		align-items: baseline;
		justify-content: flex-start;
		flex-grow: 0;
		gap: 10px;
	}
	.temp {
		font-weight: 700;
		line-height: 1.05;
		letter-spacing: -2px;
	}
	.desc {
		font-size: 18px;
		font-weight: 500;
	}
	.row .desc {
		font-size: 16px;
	}
	.sum {
		flex: none;
		display: flex;
		flex-direction: column;
		gap: 6px;
		font-size: 15px;
	}
	.sum.one {
		margin-top: 4px;
		font-size: 14px;
	}
	.sum p {
		margin: 0;
	}
	.sum.one p {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
</style>
