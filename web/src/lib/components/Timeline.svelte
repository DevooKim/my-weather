<script lang="ts">
	import { dayLabel, kst, round1 } from '$lib/format';
	import type { Timeline } from '$lib/types';
	import { iconColor, slotIcon } from '$lib/weather';
	import Icon from './Icon.svelte';

	let {
		tl,
		selected,
		meteo = false,
		cw = 44,
		onselect,
		onhover
	}: {
		tl: Timeline;
		selected: number;
		meteo?: boolean;
		cw?: number;
		onselect: (i: number) => void;
		onhover?: (i: number | null) => void;
	} = $props();

	let scroller: HTMLDivElement | undefined = $state();

	const slots = $derived(tl.slots);
	const n = $derived(slots.length);
	const W = $derived(n * cw);

	const nowIdx = $derived.by(() => {
		const i = slots.findIndex((s) => s.phase === 'now');
		if (i >= 0) return i;
		const f = slots.findIndex((s) => s.phase === 'future');
		return f >= 0 ? f : n - 1;
	});

	// 세로 배치
	const TAG = 18,
		DAY = 20,
		ICON = 30,
		CHART = 64,
		TEMP = 18,
		PCP = 36,
		POP = 20,
		ARW = 24,
		WND = 18,
		HUM = 20,
		HOUR = 20;
	const yDay = TAG;
	const yIcon = yDay + DAY;
	const yChart = yIcon + ICON;
	const yTemp = yChart + CHART;
	const yPcp = yTemp + TEMP;
	const yPop = yPcp + PCP;
	const yArw = yPop + POP;
	const yWnd = yArw + ARW;
	const yHum = yWnd + WND;
	const yHour = $derived(meteo ? yHum + HUM : yPcp);
	const H = $derived(yHour + HOUR);

	const cx = (i: number) => i * cw + cw / 2;
	// 지금 칸의 왼쪽 경계. 칸 가운데에 두면 글자 위를 지나간다.
	const nowX = $derived(nowIdx * cw);

	const temps = $derived(slots.map((s) => s.tmp).filter((t): t is number => t != null));
	const tmax = $derived(temps.length ? Math.max(...temps) : 1);
	const tmin = $derived(temps.length ? Math.min(...temps) : 0);
	const pad = 12;
	const yOf = (t: number) => yChart + pad + ((tmax - t) / Math.max(1, tmax - tmin)) * (CHART - 2 * pad);

	type Seg = { pts: string; kind: 'past' | 'est' | 'fut' };
	const segs = $derived.by(() => {
		const out: Seg[] = [];
		let cur: Seg | null = null;
		for (let i = 0; i < n - 1; i++) {
			const a = slots[i],
				b = slots[i + 1];
			if (a.tmp == null || b.tmp == null) {
				cur = null;
				continue;
			}
			const kind: Seg['kind'] = i + 1 <= nowIdx ? (a.estimated || b.estimated ? 'est' : 'past') : 'fut';
			const pa = `${cx(i).toFixed(1)},${yOf(a.tmp).toFixed(1)}`;
			const pb = `${cx(i + 1).toFixed(1)},${yOf(b.tmp).toFixed(1)}`;
			if (cur && cur.kind === kind) cur.pts += ' ' + pb;
			else {
				cur = { pts: pa + ' ' + pb, kind };
				out.push(cur);
			}
		}
		return out;
	});

	const cols = $derived(
		slots.map((s, i) => {
			const p = kst(s.t);
			const dayStart = i === 0 || p.h === 0;
			const icon = slotIcon(s, tl.daily);
			const mm = s.wet && s.pcp ? s.pcp.mm : 0;
			return {
				s,
				i,
				x: i * cw,
				dayStart,
				dayLabel: dayStart ? dayLabel(s.t, tl.now) : '',
				hour: String(p.h).padStart(2, '0'),
				icon,
				mm,
				mmLabel: mm > 0 ? (s.pcp?.code != null ? '' : String(round1(mm))) : '',
				code: s.pcp?.code ?? 0
			};
		})
	);

	const band = $derived.by(() => {
		let a = -1,
			b = -1;
		for (let i = nowIdx; i < n; i++) {
			if (slots[i].h === 1) {
				if (a < 0) a = i;
				b = i;
			} else if (a >= 0) break;
		}
		return a >= 0 ? { x: a * cw, w: (b - a + 1) * cw } : null;
	});

	export function scrollToIndex(i: number, where = 0.3, smooth = true) {
		if (!scroller) return;
		const left = Math.max(0, cx(i) - scroller.clientWidth * where);
		scroller.scrollTo({ left, behavior: smooth ? 'smooth' : 'auto' });
	}

	function ensureVisible(i: number) {
		if (!scroller) return;
		const x = cx(i);
		if (x < scroller.scrollLeft + cw || x > scroller.scrollLeft + scroller.clientWidth - cw) scrollToIndex(i, 0.5);
	}

	// 처음 그릴 때와 장소가 바뀔 때 '지금'(또는 선택 칸)으로 넘긴다
	let shownFor = '';
	$effect(() => {
		const key = `${tl.place.nx},${tl.place.ny},${tl.place.region ?? ''}`;
		if (scroller && n && shownFor !== key) {
			shownFor = key;
			scrollToIndex(selected >= 0 ? selected : nowIdx, 0.3, false);
		}
	});

	function key(e: KeyboardEvent) {
		let i = selected;
		if (e.key === 'ArrowRight') i = Math.min(n - 1, selected + 1);
		else if (e.key === 'ArrowLeft') i = Math.max(0, selected - 1);
		else if (e.key === 'Home') i = 0;
		else if (e.key === 'End') i = n - 1;
		else return;
		e.preventDefault();
		onselect(i);
		ensureVisible(i);
	}

	const wsdShort = (s: (typeof slots)[number]) =>
		s.wsd != null ? String(Math.round(s.wsd)) : s.wsd_code ? ['', '약', '중', '강'][s.wsd_code] ?? '' : '';
</script>

<div class="scroller" bind:this={scroller}>
	<svg
		width={W}
		height={H}
		viewBox="0 0 {W} {H}"
		role="slider"
		tabindex="0"
		aria-label="시간별 타임라인. 왼쪽 오른쪽 화살표로 칸을 고릅니다"
		aria-valuemin={0}
		aria-valuemax={n - 1}
		aria-valuenow={selected}
		aria-valuetext={cols[selected] ? `${cols[selected].dayLabel || dayLabel(slots[selected].t, tl.now)} ${cols[selected].hour}시` : ''}
		onkeydown={key}
		onpointerleave={() => onhover?.(null)}
	>
		<!-- 1시간 띠, 실측(과거) 배경 -->
		{#if band}
			<rect x={band.x} y={TAG} width={band.w} height={H - TAG} fill="var(--tl-band)" />
		{/if}
		<rect x="0" y={TAG} width={Math.max(0, nowX)} height={H - TAG} fill="var(--tl-past)" />

		<!-- 날짜 구분선 -->
		{#each cols as c (c.i)}
			{#if c.dayStart && c.i > 0}
				<line x1={c.x + 0.5} x2={c.x + 0.5} y1={TAG} y2={H} stroke="var(--line-2)" />
			{/if}
		{/each}

		<!-- 선택 칸 -->
		{#if selected >= 0 && selected < n}
			<rect
				x={selected * cw + 1.5}
				y={yIcon}
				width={cw - 3}
				height={H - yIcon - 1.5}
				rx="6"
				fill="none"
				stroke="var(--accent)"
				stroke-width="2"
			/>
		{/if}

		<!-- 지금 세로선 -->
		<line x1={nowX} x2={nowX} y1={2} y2={H} stroke="var(--accent)" stroke-width="2" />
		<rect x={Math.max(0, nowX - 15)} y="1" width="30" height="16" rx="4" fill="#2A72E5" />
		<text x={Math.max(15, nowX)} y="13" text-anchor="middle" class="tag">지금</text>

		{#each cols as c (c.i)}
			{#if c.dayLabel}
				<text x={c.x + 4} y={yDay + 14} class="day" class:today={c.dayLabel.startsWith('오늘')}>{c.dayLabel}</text>
			{/if}
			<g transform="translate({c.x + cw / 2 - 10},{yIcon + 5})">
				<Icon name={c.icon} size={20} color={iconColor(c.icon)} />
			</g>
			{#if c.s.tmp != null}
				<text x={c.x + cw / 2} y={yTemp + 13} text-anchor="middle" class="temp" class:pastt={c.i < nowIdx}
					>{Math.round(c.s.tmp)}°</text
				>
			{/if}
			{#if meteo}
				{#if c.mm > 0 || c.code > 0}
					{@const bh = c.code > 0 ? [0, 5, 12, 22][c.code] : Math.min(22, Math.max(2, c.mm * 6))}
					<rect
						x={c.x + cw / 2 - Math.max(6, cw * 0.45) / 2}
						y={yPcp + PCP - bh}
						width={Math.max(6, cw * 0.45)}
						height={bh}
						rx="2"
						fill="var(--bar-rain)"
					/>
					<text x={c.x + cw / 2} y={yPcp + PCP - bh - 3} text-anchor="middle" class="small" fill="var(--accent-text)"
						>{c.mmLabel}</text
					>
				{/if}
				<text x={c.x + cw / 2} y={yPop + 14} text-anchor="middle" class="small" fill="var(--accent-text)"
					>{c.s.pop != null ? `${c.s.pop}%` : ''}</text
				>
				{#if c.s.vec != null && (c.s.wsd == null || c.s.wsd >= 0.5)}
					<g transform="translate({c.x + cw / 2},{yArw + ARW / 2}) rotate({(c.s.vec + 180) % 360}) translate(-7,-7)">
						<Icon name="arrow" size={14} color="var(--text-3)" />
					</g>
				{/if}
				<text x={c.x + cw / 2} y={yWnd + 13} text-anchor="middle" class="small" fill="var(--text-3)">{wsdShort(c.s)}</text>
				<text x={c.x + cw / 2} y={yHum + 14} text-anchor="middle" class="small" fill="var(--text-3)"
					>{c.s.reh != null ? `${Math.round(c.s.reh)}%` : ''}</text
				>
			{/if}
			<text x={c.x + cw / 2} y={yHour + 14} text-anchor="middle" class="hour">{c.hour}</text>
		{/each}

		{#if meteo}
			<line x1="0" x2={W} y1={yPcp + 0.5} y2={yPcp + 0.5} stroke="var(--line)" />
			<line x1="0" x2={W} y1={yArw + 0.5} y2={yArw + 0.5} stroke="var(--line)" />
			<line x1="0" x2={W} y1={yHum + 0.5} y2={yHum + 0.5} stroke="var(--line)" />
			<text x="4" y={yPcp + 12} class="small" fill="var(--text-3)">강수량 mm</text>
			<text x="4" y={yArw + 15} class="small" fill="var(--text-3)">바람</text>
			<text x="4" y={yHum + 14} class="small" fill="var(--text-3)">습도</text>
		{/if}
		<line x1="0" x2={W} y1={yHour + 0.5} y2={yHour + 0.5} stroke="var(--line)" />

		<!-- 기온 선 -->
		{#each segs as sg, k (k)}
			<polyline
				points={sg.pts}
				fill="none"
				stroke={sg.kind === 'fut' ? 'var(--text)' : 'var(--tl-pastline)'}
				stroke-width="2"
				stroke-linejoin="round"
				stroke-dasharray={sg.kind === 'est' ? '4 3' : undefined}
			/>
		{/each}
		{#each cols as c (c.i)}
			{#if c.s.tmp != null}
				<circle
					cx={c.x + cw / 2}
					cy={yOf(c.s.tmp)}
					r="3"
					fill={c.i < nowIdx ? 'var(--tl-pastline)' : 'var(--text)'}
					opacity={c.s.estimated ? 0.6 : 1}
				/>
			{/if}
		{/each}
		<text x="4" y={yChart + 10} class="small" fill="var(--text-3)">{slots[0]?.estimated ? '추정' : '실측'}</text>
		<text x={nowX + cw + 4} y={yChart + 10} class="small" fill="var(--accent-text)"
			>예보{band && band.x === nowX ? ' · 1시간 간격' : ''}</text
		>
		{#if band && band.x + band.w < W}
			<text x={band.x + band.w + 4} y={yChart + 10} class="small" fill="var(--text-3)">3시간 간격</text>
		{/if}

		<!-- 누르는 영역 -->
		{#each cols as c (c.i)}
			<rect
				x={c.x}
				y={TAG}
				width={cw}
				height={H - TAG}
				fill="transparent"
				class="hit"
				role="presentation"
				onclick={() => onselect(c.i)}
				onpointerenter={(e) => e.pointerType === 'mouse' && onhover?.(c.i)}
			/>
		{/each}
	</svg>
</div>

<style>
	.scroller {
		overflow-x: auto;
		overflow-y: hidden;
		-webkit-overflow-scrolling: touch;
		overscroll-behavior-x: contain;
		scrollbar-width: thin;
	}
	svg {
		display: block;
		font-family: inherit;
		user-select: none;
		-webkit-user-select: none;
		touch-action: pan-x pan-y;
		outline: none;
	}
	svg:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: -2px;
	}
	text {
		font-size: 11px;
	}
	.small {
		font-size: 10px;
	}
	.tag {
		font-size: 10px;
		font-weight: 700;
		fill: #fff;
	}
	.day {
		fill: var(--text-3);
	}
	.day.today {
		fill: var(--text);
		font-weight: 700;
	}
	.temp {
		fill: var(--text);
		font-weight: 500;
	}
	.temp.pastt {
		fill: var(--text-2);
	}
	.hour {
		fill: var(--text-3);
	}
	.hit {
		cursor: pointer;
	}
</style>
