<script lang="ts">
	import { dayLabel, kst, round1 } from '$lib/format';
	import type { Timeline } from '$lib/types';
	import { iconColor, slotIcon, tempColor } from '$lib/weather';
	import Icon from './Icon.svelte';

	let {
		tl,
		selected,
		meteo = false,
		cw = 52,
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
	const TAG = 20,
		DAY = 22,
		ICON = 38,
		CHART = 96,
		TEMP = 22,
		PCP = 40,
		POP = 22,
		ARW = 26,
		WND = 20,
		HUM = 22,
		HOUR = 22,
		LBL = 16,
		BOTTOM = 6;
	// 위에서부터: 지금 표시, 날짜, 시각, 아이콘, 기온 선, 기온 숫자, (기상 줄들)
	const yDay = TAG;
	const yHour = yDay + DAY;
	const yIcon = yHour + HOUR;
	const yChart = yIcon + ICON;
	const yTemp = yChart + CHART;
	// 기상 줄마다 위에 이름 띠(LBL)를 따로 둔다. 값과 같은 줄에 이름을 쓰면 첫 칸 값과 겹친다
	const yPcpL = yTemp + TEMP;
	const yPcp = yPcpL + LBL;
	const yPop = yPcp + PCP;
	const yArwL = yPop + POP;
	const yArw = yArwL + LBL;
	const yWnd = yArw + ARW;
	const yHumL = yWnd + WND;
	const yHum = yHumL + LBL;
	const H = $derived((meteo ? yHum + HUM : yPcpL) + BOTTOM);
	const ROWS = [
		{ y: yPcpL, label: '강수 mm · 확률' },
		{ y: yArwL, label: '바람 m/s' },
		{ y: yHumL, label: '습도' }
	];

	const cx = (i: number) => i * cw + cw / 2;
	// 지금 칸의 왼쪽 경계. 칸 가운데에 두면 글자 위를 지나간다.
	const nowX = $derived(nowIdx * cw);

	const temps = $derived(slots.map((s) => s.tmp).filter((t): t is number => t != null));
	const tmax = $derived(temps.length ? Math.max(...temps) : 1);
	const tmin = $derived(temps.length ? Math.min(...temps) : 0);
	const pad = 12;
	const yOf = (t: number) => yChart + pad + ((tmax - t) / Math.max(1, tmax - tmin)) * (CHART - 2 * pad);

	// 기온 선: 단조 3차 곡선(Fritsch–Carlson). 값 사이를 부드럽게 잇되 실제 값보다 튀어 오르지 않는다.
	// 실측·추정·예보 구간마다 따로 그리지만 접선은 전체에서 구해서 이음매가 꺾이지 않는다.
	type Seg = { d: string; area: string; kind: 'past' | 'est' | 'fut' };
	const segs = $derived.by(() => {
		const ys = slots.map((s) => (s.tmp == null ? null : yOf(s.tmp)));
		const dk = (i: number) => (ys[i] != null && ys[i + 1] != null ? (ys[i + 1]! - ys[i]!) / cw : null);
		const m = ys.map((_, i) => {
			const a = i > 0 ? dk(i - 1) : null,
				b = dk(i);
			if (a == null) return b ?? 0;
			if (b == null) return a;
			if (a * b <= 0) return 0;
			// 조화 평균: 단조성을 지키면서 가파른 쪽에 덜 끌려간다
			return (2 * a * b) / (a + b);
		});
		const base = yChart + CHART;
		const out: Seg[] = [];
		let cur: Seg | null = null;
		let x0 = 0,
			curEnd = 0;
		const close = () => {
			if (cur) cur.area += ` L${cx(curEnd).toFixed(1)},${base} L${x0.toFixed(1)},${base} Z`;
		};
		for (let i = 0; i < n - 1; i++) {
			const a = ys[i],
				b = ys[i + 1];
			if (a == null || b == null) {
				close();
				cur = null;
				continue;
			}
			const kind: Seg['kind'] =
				i + 1 <= nowIdx ? (slots[i].estimated || slots[i + 1].estimated ? 'est' : 'past') : 'fut';
			const xa = cx(i),
				xb = cx(i + 1),
				h = cw / 3;
			const c = ` C${(xa + h).toFixed(1)},${(a + m[i] * h).toFixed(1)} ${(xb - h).toFixed(1)},${(b - m[i + 1] * h).toFixed(1)} ${xb.toFixed(1)},${b.toFixed(1)}`;
			if (!cur || cur.kind !== kind) {
				close();
				const mv = `M${xa.toFixed(1)},${a.toFixed(1)}`;
				cur = { d: mv, area: mv, kind };
				x0 = xa;
				out.push(cur);
			}
			cur.d += c;
			cur.area += c;
			curEnd = i + 1;
		}
		close();
		return out;
	});

	// 선 색: 기온 눈금을 차트 높이에 그대로 깐다(위 = 최고, 아래 = 최저)
	const gradStops = $derived.by(() => {
		const k = 6;
		return Array.from({ length: k + 1 }, (_, j) => {
			const off = j / k;
			return { off, color: tempColor(tmax - (tmax - tmin) * off) };
		});
	});
	const gy1 = $derived(yChart + pad);
	const gy2 = $derived(yChart + CHART - pad);
	const uid = Math.random().toString(36).slice(2, 8);

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

	// 처음 그릴 때와 장소가 바뀔 때 '지금'(또는 선택 칸)으로 넘긴다.
	// 새 자료가 오면(저장해 둔 타임라인 → 새로 받은 타임라인, 5분마다 갱신, 앱으로 돌아옴) '지금' 칸이
	// 옮겨 가므로 다시 맞춘다. 사용자가 직접 넘겨 본 뒤에는 그대로 둔다(1분 넘게 다른 앱에 있다 오면 다시 따라감).
	let shownFor = '';
	let shownNow = '';
	let userMoved = false;
	$effect(() => {
		const key = `${tl.place.nx},${tl.place.ny},${tl.place.region ?? ''}`;
		if (!scroller || !n) return;
		if (shownFor !== key) {
			shownFor = key;
			shownNow = tl.now;
			userMoved = false;
			scrollToIndex(selected >= 0 ? selected : nowIdx, 0.3, false);
		} else if (shownNow !== tl.now) {
			shownNow = tl.now;
			if (!userMoved && selected === nowIdx) scrollToIndex(nowIdx, 0.3, false);
		}
	});
	$effect(() => {
		let hiddenAt = 0;
		const onVis = () => {
			if (document.visibilityState === 'hidden') hiddenAt = Date.now();
			else if (hiddenAt && Date.now() - hiddenAt > 60_000) userMoved = false;
		};
		document.addEventListener('visibilitychange', onVis);
		return () => document.removeEventListener('visibilitychange', onVis);
	});
	const moved = () => (userMoved = true);

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

<div class="scroller" role="presentation" bind:this={scroller} onpointerdown={moved} onwheel={moved} ontouchstart={moved}>
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
		<defs>
			<linearGradient id="tg-{uid}" gradientUnits="userSpaceOnUse" x1="0" x2="0" y1={gy1} y2={gy2}>
				{#each gradStops as g (g.off)}<stop offset={g.off} stop-color={g.color} />{/each}
			</linearGradient>
			<linearGradient id="ta-{uid}" gradientUnits="userSpaceOnUse" x1="0" x2="0" y1={gy1} y2={yChart + CHART}>
				{#each gradStops as g (g.off)}<stop
						offset={g.off}
						stop-color={g.color}
						stop-opacity={0.26 * (1 - g.off) + 0.02}
					/>{/each}
			</linearGradient>
			<linearGradient id="rb-{uid}" x1="0" x2="0" y1="0" y2="1">
				<stop offset="0" stop-color="var(--bar-rain)" />
				<stop offset="1" stop-color="var(--bar-rain)" stop-opacity="0.45" />
			</linearGradient>
			<linearGradient id="nl-{uid}" gradientUnits="userSpaceOnUse" x1="0" x2="0" y1="0" y2={H}>
				<stop offset="0" stop-color="var(--accent)" />
				<stop offset="1" stop-color="var(--accent)" stop-opacity="0.1" />
			</linearGradient>
		</defs>

		<!-- 1시간 띠, 실측(과거) 배경 -->
		{#if band}
			<rect x={band.x} y={TAG} width={band.w} height={H - TAG} fill="var(--tl-band)" />
		{/if}
		<rect x="0" y={TAG} width={Math.max(0, nowX)} height={H - TAG} fill="var(--tl-past)" />

		<!-- 날짜 구분선 -->
		{#each cols as c (c.i)}
			{#if c.dayStart && c.i > 0}
				<line x1={c.x + 0.5} x2={c.x + 0.5} y1={TAG + 4} y2={H} stroke="var(--line-2)" stroke-dasharray="2 4" />
			{/if}
		{/each}

		<!-- 선택 칸: 옅게 채운 둥근 기둥 -->
		{#if selected >= 0 && selected < n}
			<rect
				x={selected * cw + 3}
				y={yHour - 2}
				width={cw - 6}
				height={H - yHour - 2}
				rx={(cw - 6) / 2}
				class="sel"
			/>
		{/if}

		<!-- 지금 세로선 -->
		<line x1={nowX} x2={nowX} y1={TAG} y2={H} stroke="url(#nl-{uid})" stroke-width="1.5" />
		<rect x={Math.max(0, nowX - 18)} y="1" width="36" height="18" rx="9" fill="var(--accent)" />
		<text x={Math.max(18, nowX)} y="14" text-anchor="middle" class="tag">지금</text>

		{#each cols as c (c.i)}
			{#if c.dayLabel}
				<text x={c.x + 6} y={yDay + 15} class="day" class:today={c.dayLabel.startsWith('오늘')}>{c.dayLabel}</text>
			{/if}
			<g transform="translate({c.x + cw / 2 - 12},{yIcon + 7})" opacity={c.i < nowIdx ? 0.55 : 1}>
				<Icon name={c.icon} size={24} color={iconColor(c.icon)} />
			</g>
			{#if c.s.tmp != null}
				<text
					x={c.x + cw / 2}
					y={yTemp + 16}
					text-anchor="middle"
					class="temp"
					class:pastt={c.i < nowIdx}
					class:selt={c.i === selected}
					><!-- 보이지 않는 °를 앞에 두어 숫자 자체가 칸 가운데(아이콘 아래)에 선다 --><tspan fill-opacity="0"
						>°</tspan
					>{Math.round(c.s.tmp)}°</text
				>
			{/if}
			{#if meteo}
				{#if c.mm > 0 || c.code > 0}
					{@const bh = c.code > 0 ? [0, 5, 12, 22][c.code] : Math.min(22, Math.max(2, c.mm * 6))}
					{@const bw = Math.max(6, cw * 0.4)}
					<rect
						x={c.x + cw / 2 - bw / 2}
						y={yPcp + PCP - bh - 2}
						width={bw}
						height={bh}
						rx={Math.min(3, bh / 2)}
						fill="url(#rb-{uid})"
					/>
					<text x={c.x + cw / 2} y={yPcp + PCP - bh - 5} text-anchor="middle" class="small" fill="var(--accent-text)"
						>{c.mmLabel}</text
					>
				{/if}
				<text x={c.x + cw / 2} y={yPop + 15} text-anchor="middle" class="small" fill="var(--accent-text)"
					>{c.s.pop != null ? `${c.s.pop}%` : ''}</text
				>
				{#if c.s.vec != null && (c.s.wsd == null || c.s.wsd >= 0.5)}
					<g transform="translate({c.x + cw / 2},{yArw + ARW / 2}) rotate({(c.s.vec + 180) % 360}) translate(-7,-7)">
						<Icon name="arrow" size={14} color="var(--text-3)" />
					</g>
				{/if}
				<text x={c.x + cw / 2} y={yWnd + 14} text-anchor="middle" class="small" fill="var(--text-3)">{wsdShort(c.s)}</text>
				<text x={c.x + cw / 2} y={yHum + 14} text-anchor="middle" class="small" fill="var(--text-3)"
					>{c.s.reh != null ? `${Math.round(c.s.reh)}%` : ''}</text
				>
			{/if}
			<text x={c.x + cw / 2} y={yHour + 15} text-anchor="middle" class="hour" class:selh={c.i === selected}
				>{c.hour}</text
			>
		{/each}

		{#if meteo}
			<!-- 줄 이름: 날짜가 바뀌는 칸마다 되풀이해서 옆으로 넘겨도 보인다 -->
			{#each ROWS as r (r.label)}
				<line x1="0" x2={W} y1={r.y + 0.5} y2={r.y + 0.5} stroke="var(--line)" />
				{#each cols as c (c.i)}
					{#if c.dayStart}
						<text x={c.x + 6} y={r.y + 12} class="rowl">{r.label}</text>
					{/if}
				{/each}
			{/each}
		{/if}

		<!-- 기온 선: 아래로 번지는 면 + 기온 색 선. 지난 구간은 옅게, 추정은 점선 -->
		{#each segs as sg, k (k)}
			<path d={sg.area} fill="url(#ta-{uid})" opacity={sg.kind === 'fut' ? 1 : 0.45} />
		{/each}
		{#each segs as sg, k (k)}
			<path
				d={sg.d}
				fill="none"
				stroke="url(#tg-{uid})"
				stroke-width={sg.kind === 'fut' ? 2.5 : 2}
				stroke-linecap="round"
				stroke-linejoin="round"
				stroke-dasharray={sg.kind === 'est' ? '3 4' : undefined}
				opacity={sg.kind === 'fut' ? 1 : 0.5}
			/>
		{/each}
		{#if slots[selected]?.tmp != null}
			{@const st = slots[selected].tmp as number}
			<circle cx={cx(selected)} cy={yOf(st)} r="7" fill={tempColor(st)} opacity="0.22" />
			<circle cx={cx(selected)} cy={yOf(st)} r="4" fill="var(--page)" stroke={tempColor(st)} stroke-width="2.5" />
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
		scrollbar-width: none;
	}
	.scroller::-webkit-scrollbar {
		display: none;
	}
	svg {
		display: block;
		font-family: inherit;
		font-variant-numeric: tabular-nums;
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
		font-size: 13px;
	}
	.small {
		font-size: 11px;
		font-weight: 500;
	}
	.rowl {
		font-size: 10px;
		font-weight: 600;
		fill: var(--text-4);
	}
	.tag {
		font-size: 11px;
		font-weight: 700;
		fill: #fff;
	}
	.sel {
		fill: var(--tl-sel);
	}
	.day {
		font-size: 12px;
		font-weight: 600;
		fill: var(--text-3);
	}
	.day.today {
		fill: var(--text);
		font-weight: 700;
	}
	.temp {
		fill: var(--text);
		font-size: 14px;
		font-weight: 600;
	}
	.temp.pastt {
		fill: var(--text-3);
		font-weight: 500;
	}
	.temp.selt {
		fill: var(--text);
		font-weight: 800;
	}
	.hour {
		fill: var(--text-3);
		font-weight: 500;
	}
	.hour.selh {
		fill: var(--accent-text);
		font-weight: 700;
	}
	.hit {
		cursor: pointer;
	}
</style>
