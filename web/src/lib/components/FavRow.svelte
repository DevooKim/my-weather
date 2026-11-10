<script lang="ts">
	import { app } from '$lib/app.svelte';
	import { district, shortFull, summaryLines } from '$lib/format';
	import { sceneOf } from '$lib/scene';
	import type { Place } from '$lib/types';
	import Icon from './Icon.svelte';
	import WeatherCanvas from './WeatherCanvas.svelte';

	let {
		place,
		index,
		count,
		current,
		nowText,
		admin,
		shift,
		dragging,
		card,
		starred = true,
		onpick,
		onstar,
		onbell,
		ondrag
	}: {
		place: Place;
		index: number;
		count: number;
		current: boolean;
		nowText: string;
		admin: boolean;
		shift: number;
		dragging: boolean;
		card: boolean;
		starred?: boolean;
		onpick: () => void;
		onstar: () => void;
		onbell: () => void;
		ondrag: (from: number, to: number | null, done: boolean) => void;
	} = $props();

	const DEL = 88;
	let el: HTMLLIElement | undefined = $state();
	let dx = $state(0);
	let open = $state(false);
	let dy = $state(0);
	let rowH = $state(56);

	// 밀어서 삭제
	let sx = 0,
		sy = 0,
		moved = false;
	let axis = $state<'x' | 'y' | null>(null);
	let tracking = $state(false);
	function sdown(e: PointerEvent) {
		if ((e.target as HTMLElement).closest('[data-grip]')) return;
		if (e.pointerType === 'mouse' && e.button !== 0) return;
		tracking = true;
		moved = false;
		axis = null;
		sx = e.clientX;
		sy = e.clientY;
	}
	function smove(e: PointerEvent) {
		if (!tracking) return;
		const mx = e.clientX - sx,
			my = e.clientY - sy;
		if (!axis && Math.hypot(mx, my) > 8) {
			axis = Math.abs(mx) > Math.abs(my) ? 'x' : 'y';
			if (axis === 'x') (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
		}
		if (axis === 'x') {
			moved = true;
			dx = Math.min(0, Math.max(-DEL - 24, (open ? -DEL : 0) + mx));
		}
	}
	function sup() {
		if (!tracking) return;
		tracking = false;
		if (axis === 'x') {
			open = dx < -DEL / 2;
			dx = 0;
		}
		axis = null;
	}
	function pick(e: MouseEvent) {
		if (moved) {
			e.preventDefault();
			moved = false;
			return;
		}
		if (open) {
			open = false;
			return;
		}
		onpick();
	}

	// 끌어서 순서
	let gy = 0,
		gActive = false;
	function gdown(e: PointerEvent) {
		gActive = true;
		gy = e.clientY;
		rowH = (el?.offsetHeight ?? 52) + 4;
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
		e.preventDefault();
	}
	function gmove(e: PointerEvent) {
		if (!gActive) return;
		dy = e.clientY - gy;
		const to = Math.max(0, Math.min(count - 1, index + Math.round(dy / rowH)));
		ondrag(index, to, false);
	}
	function gup() {
		if (!gActive) return;
		gActive = false;
		const to = Math.max(0, Math.min(count - 1, index + Math.round(dy / rowH)));
		dy = 0;
		ondrag(index, to, true);
	}
	function gkey(e: KeyboardEvent) {
		if (e.key === 'ArrowUp' && index > 0) ondrag(index, index - 1, true);
		else if (e.key === 'ArrowDown' && index < count - 1) ondrag(index, index + 1, true);
		else return;
		e.preventDefault();
	}

	const bellOn = $derived(!!place.alerts && (place.alerts.soon || place.alerts.eve));
	const sub = $derived(district(place.full) || shortFull(place.full, ''));
	const scene = $derived(card && current ? (app.demo?.scene ?? sceneOf(app.timeline)) : null);
	const sumLine = $derived(card && current && app.timeline ? summaryLines(app.timeline.summary.text).join(' · ') : '');
</script>

<li
	bind:this={el}
	class="fav"
	class:card
	class:current
	class:dragging
	style="transform: translateY({dragging ? dy : shift * rowH}px)"
>
	{#if dx < 0 || open}
		<div class="del">
			<button type="button" onclick={() => app.removeFav(place)}>삭제</button>
		</div>
	{/if}
	<div
		class="front"
		style="transform: translateX({dx || (open ? -DEL : 0)}px); transition: {tracking && axis === 'x' ? 'none' : 'transform .2s'}"
		role="presentation"
		onpointerdown={sdown}
		onpointermove={smove}
		onpointerup={sup}
		onpointercancel={sup}
	>
		{#if scene}
			<WeatherCanvas {scene} />
		{/if}
		{#if count > 1}
			<span
				class="grip"
				data-grip
				role="button"
				tabindex="0"
				aria-label="{place.name} 순서 바꾸기. 위아래 화살표"
				onpointerdown={gdown}
				onpointermove={gmove}
				onpointerup={gup}
				onpointercancel={gup}
				onkeydown={gkey}
			>
				<Icon name="grip" size={16} color={scene ? 'rgba(255,255,255,.7)' : 'var(--text-4)'} />
			</span>
		{:else}
			<span class="grip-space"></span>
		{/if}
		<button type="button" class="name" onclick={pick} aria-current={current ? 'true' : undefined}>
			<span class="line1"><b>{place.name}</b>{#if sub}<span class="sub">&nbsp;· {sub}</span>{/if}</span>
			{#if card && current && app.timeline}
				{@const n = app.timeline.summary.now}
				<span class="big">{n?.tmp != null ? `${Math.round(n.tmp)}°` : ''} <span class="w">{nowText.replace(/^-?\d+°\s*/, '')}</span></span>
				{#if sumLine}<span class="sum">{sumLine}</span>{/if}
			{:else if nowText}
				<span class="now">{nowText}</span>
			{/if}
		</button>
		{#if admin && place.id != null}
			<button
				type="button"
				class="icon-btn"
				aria-label="{place.name} 비 알림 {bellOn ? '끄기' : '켜기'}"
				aria-pressed={bellOn}
				onclick={onbell}
			>
				<Icon
					name={bellOn ? 'bell-fill' : 'bell'}
					size={20}
					color={scene ? '#fff' : bellOn ? 'var(--accent)' : 'var(--text-3)'}
				/>
			</button>
		{/if}
		<button type="button" class="icon-btn" aria-label="{place.name} {starred ? '즐겨찾기에서 빼기' : '즐겨찾기에 추가'}" aria-pressed={starred} onclick={onstar}>
			<Icon name={starred ? 'star-fill' : 'star'} size={22} color={scene ? (starred ? '#FFD66B' : '#fff') : starred ? 'var(--star)' : 'var(--text-3)'} />
		</button>
	</div>
</li>

<style>
	.fav {
		position: relative;
		border-radius: 10px;
		overflow: hidden;
		min-height: 52px;
		transition: transform 0.18s;
	}
	.fav.dragging {
		transition: none;
		z-index: 2;
		box-shadow: 0 6px 18px rgba(0, 0, 0, 0.2);
	}
	.del {
		position: absolute;
		top: 0;
		bottom: 0;
		right: 0;
		width: 88px;
		background: var(--danger);
		display: flex;
		align-items: center;
		justify-content: center;
	}
	.del button {
		min-height: 44px;
		width: 100%;
		background: none;
		border: 0;
		color: #fff;
		font: inherit;
		font-size: 14px;
		font-weight: 700;
	}
	.front {
		position: relative;
		display: flex;
		align-items: center;
		gap: 4px;
		min-height: 52px;
		padding-right: 4px;
		background: var(--surface);
		touch-action: pan-y;
	}
	.current .front {
		background: var(--accent-weak);
	}
	.grip,
	.grip-space {
		position: relative;
		width: 44px;
		align-self: stretch;
		display: flex;
		align-items: center;
		justify-content: center;
		flex: none;
		touch-action: none;
		cursor: grab;
	}
	.grip-space {
		width: 12px;
	}
	.name {
		position: relative;
		flex-grow: 1;
		min-width: 0;
		min-height: 44px;
		padding: 0;
		background: none;
		border: 0;
		font: inherit;
		text-align: left;
		color: var(--text);
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		cursor: pointer;
	}
	.line1 {
		font-size: 15px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.sub {
		font-size: 13px;
		color: var(--text-2);
	}
	.now {
		font-size: 15px;
		font-weight: 500;
		flex: none;
	}
	.icon-btn {
		position: relative;
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
	/* 데스크톱 왼쪽 카드 */
	.card {
		border-radius: 12px;
		border: 1px solid var(--line-2);
	}
	.card .front {
		min-height: 56px;
		padding: 6px 4px 6px 0;
	}
	.card.current {
		border-color: transparent;
	}
	.card.current .front {
		background: #2b3440;
		color: #fff;
		min-height: 110px;
		align-items: flex-start;
	}
	.card.current .name {
		color: #fff;
		flex-direction: column;
		align-items: flex-start;
		justify-content: center;
		gap: 2px;
		align-self: stretch;
	}
	.card.current .sub {
		color: rgba(255, 255, 255, 0.85);
	}
	.big {
		font-size: 36px;
		font-weight: 700;
		line-height: 1.1;
	}
	.big .w {
		font-size: 15px;
		font-weight: 500;
	}
	.sum {
		font-size: 13px;
		white-space: normal;
	}
	.card.current .grip {
		align-self: stretch;
	}
</style>
