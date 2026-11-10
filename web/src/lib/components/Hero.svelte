<script lang="ts">
	// 1A(큰 현재 날씨) → 1C(접힌 크기) → 한 줄 머리. 높이 h는 부모가 스크롤로 정한다.
	import { app } from '$lib/app.svelte';
	import { airText, deg, nowPrecipText, shortFull, summaryLines, weatherWord } from '$lib/format';
	import type { Scene } from '$lib/scene';
	import Icon from './Icon.svelte';
	import WeatherCanvas from './WeatherCanvas.svelte';
	import { onMount } from 'svelte';

	let {
		full,
		bar,
		scene,
		onplace,
		onmenu
	}: {
		full: number;
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
	// 미세먼지는 나쁨 이상일 때만 머리에 붙인다(좋음·보통은 상세 패널에서)
	const airBad = $derived((tl?.air?.grade ?? 0) >= 3 ? airText(tl?.air) : '');
	const fogWord = $derived(tl?.fog?.fog ? '안개' : '');
	const desc1A = $derived(
		[precip ? precip.replace(/^(\S+) /, '$1 · 시간당 ') : word, fogWord, feels, airBad].filter(Boolean).join(' · ')
	);
	const desc1C = $derived(precip || word);

	const placeLabel = $derived.by(() => {
		const p = app.place;
		if (!p) return '장소 고르기';
		const sf = shortFull(p.full, '');
		if (!sf) return p.name || `격자 ${p.nx},${p.ny}`;
		return sf.endsWith(p.name) ? sf : `${p.name} · ${sf}`;
	});

	// 접힘 정도 p (0 = 1A, 1 = 한 줄). 높이는 바꾸지 않는다: 머리는 position: sticky로 스크롤과 함께
	// 브라우저가 직접 밀어 올리고(합성 단계라 손가락을 그대로 따라간다), JS는 투명도·크기용 CSS 변수만 바꾼다.
	let el: HTMLElement | undefined = $state();
	onMount(() => {
		let raf = 0;
		const upd = () => {
			raf = 0;
			if (!el) return;
			const p = Math.min(1, Math.max(0, window.scrollY / Math.max(1, full - bar)));
			el.style.setProperty('--p', p.toFixed(3));
			el.toggleAttribute('data-collapsed', p > 0.85);
		};
		const onScroll = () => {
			if (!raf) raf = requestAnimationFrame(upd);
		};
		upd();
		window.addEventListener('scroll', onScroll, { passive: true });
		window.addEventListener('resize', onScroll);
		return () => {
			window.removeEventListener('scroll', onScroll);
			window.removeEventListener('resize', onScroll);
			if (raf) cancelAnimationFrame(raf);
		};
	});
</script>

<header
	class="hero"
	bind:this={el}
	style="height:calc({full}px + env(safe-area-inset-top)); top:calc({bar - full}px)"
>
	<WeatherCanvas {scene} height={full + 60} />
	<div class="inner">
		<div class="top">
			<button type="button" class="place" onclick={onplace} aria-haspopup="dialog">
				<span class="ell">{placeLabel}</span><Icon name="chevron-down" size={16} />
			</button>
			<button type="button" class="menu" aria-label="메뉴" onclick={onmenu}><Icon name="menu" size={22} /></button>
		</div>
		{#if tl}
			<div class="big">
				<span class="temp">{deg(tmp)}</span>
				<span class="desc">{desc1A}</span>
			</div>
			{#if lines.length}
				<div class="sum">
					{#each lines as l, i (i)}<p>{l}</p>{/each}
				</div>
			{/if}
		{:else if app.error?.kind === 'unavailable' || app.error}
			<div class="big"><span class="desc">날씨를 불러오지 못했어요</span></div>
		{:else if app.place}
			<div class="big"><span class="desc">불러오는 중…</span></div>
		{:else}
			<div class="big"><span class="desc">장소를 골라 주세요</span></div>
		{/if}
	</div>
	<!-- 다 접혔을 때 보이는 한 줄 (머리 맨 아래 띠가 화면 위에 붙는다) -->
	<div class="barrow" style="height:{bar}px">
		<button type="button" class="place one" onclick={onplace} aria-label="장소 바꾸기. 지금 {placeLabel}">
			<span class="ell">{app.place?.name ?? '장소 고르기'}{tmp != null ? ` · ${deg(tmp)} ${desc1C}` : ''}</span>
		</button>
		<button type="button" class="menu" aria-label="메뉴" onclick={onmenu}><Icon name="menu" size={22} /></button>
	</div>
</header>

<style>
	.hero {
		--p: 0;
		position: sticky;
		left: 0;
		right: 0;
		z-index: 10;
		overflow: hidden;
		color: #fff;
		background: #2b3440;
	}
	.inner {
		position: relative;
		height: 100%;
		box-sizing: border-box;
		max-width: 720px;
		margin: 0 auto;
		padding: calc(8px + env(safe-area-inset-top)) 12px 20px 20px;
		display: flex;
		flex-direction: column;
		/* 접히는 동안 서서히 사라진다. 투명도·변형만 바꿔서 레이아웃을 다시 계산하지 않는다 */
		opacity: clamp(0, calc(1 - var(--p) * 1.4), 1);
	}
	.hero:global([data-collapsed]) .inner {
		visibility: hidden;
	}
	.top,
	.barrow {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
	}
	.barrow {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 0;
		max-width: 720px;
		margin: 0 auto;
		box-sizing: border-box;
		padding: 0 12px 0 20px;
		opacity: clamp(0, calc((var(--p) - 0.55) * 2.6), 1);
		visibility: hidden;
	}
	.hero:global([data-collapsed]) .barrow {
		visibility: visible;
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
	.temp {
		font-size: 96px;
		font-weight: 700;
		line-height: 1.05;
		letter-spacing: -2px;
		transform-origin: left center;
		transform: scale(calc(1 - var(--p) * 0.3));
		will-change: transform;
	}
	.desc {
		font-size: 18px;
		font-weight: 500;
	}
	.sum {
		flex: none;
		display: flex;
		flex-direction: column;
		gap: 6px;
		font-size: 15px;
	}
	.sum p {
		margin: 0;
	}
</style>
