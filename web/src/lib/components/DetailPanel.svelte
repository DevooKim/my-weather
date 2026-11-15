<script lang="ts">
	import { airText, amountText, dayLabel, deg, kst, weatherWord, windText } from '$lib/format';
	import type { Timeline } from '$lib/types';
	import { iconColor, slotIcon } from '$lib/weather';
	import Icon from './Icon.svelte';

	// 미세먼지 등급 점 색: 좋음·보통·나쁨·매우나쁨
	const AIR_COLOR: Record<number, string> = { 1: '#3d8bfd', 2: '#2fa36b', 3: '#e8873a', 4: '#d64545' };

	let {
		tl,
		idx,
		onnow,
		inline = false
	}: { tl: Timeline; idx: number; onnow: () => void; inline?: boolean } = $props();

	const s = $derived(tl.slots[idx]);
	const isNow = $derived(s?.phase === 'now');

	const title = $derived.by(() => {
		if (!s) return '';
		const p = kst(s.t);
		return isNow ? `지금 ${p.h}시` : `${dayLabel(s.t, tl.now)} ${p.h}시${s.h === 3 ? `~${(p.h + 3) % 24 || 24}시` : ''}`;
	});

	const word = $derived(s ? weatherWord(s) : '');
	const air = $derived(airText(tl.air));
	const pcp = $derived(s && s.wet ? amountText(s.pcp) : '');
	const sno = $derived(s && s.sno && s.sno.mm > 0 ? amountText(s.sno, 'cm') : '');

	/** 어제 같은 시각: 지금이면 요약 값, 아니면 24시간 전 시각을 담은 칸(어제가 3시간 칸이면 그 칸 값) */
	const yesterday = $derived.by(() => {
		if (!s || s.tmp == null) return null;
		if (isNow && tl.summary.yesterday_same_hour?.tmp != null) {
			const y = tl.summary.yesterday_same_hour;
			return { tmp: y.tmp as number, est: y.estimated };
		}
		const target = new Date(s.t).getTime() - 86400_000;
		const y = tl.slots.find((x) => {
			const t0 = new Date(x.t).getTime();
			return t0 <= target && target < t0 + (x.h || 1) * 3600_000;
		});
		return y && y.tmp != null ? { tmp: y.tmp, est: y.estimated } : null;
	});
	const yText = $derived.by(() => {
		if (!yesterday || !s || s.tmp == null) return '';
		const d = Math.round(s.tmp - yesterday.tmp);
		if (isNow) return `어제 ${deg(yesterday.tmp)}`;
		if (d === 0) return '어제와 비슷함';
		return `어제보다 ${Math.abs(d)}° ${d > 0 ? '높음' : '낮음'}`;
	});
	const icon = $derived(s ? slotIcon(s, tl.daily) : 'unknown');
	/** 타일용: 어제 같은 시각과의 차이 */
	const yDiff = $derived.by(() => {
		if (!yesterday || !s || s.tmp == null) return ' ';
		const d = Math.round(s.tmp - yesterday.tmp);
		return d === 0 ? '비슷함' : `${Math.abs(d)}° ${d > 0 ? '높음' : '낮음'}`;
	});
	const popText = $derived(
		s?.pop != null ? `강수확률 ${s.pop}%` : ''
	);
</script>

{#if s}
	{#if inline}
		<!-- PC: 차트 아래 카드. 머리(시각·기온·날씨) + 같은 자리에 고정된 칸들(마우스로 훑어도 칸이 움직이지 않게) -->
		<section class="inline" aria-live="polite" aria-label="선택한 칸 자세히">
			<div class="ihead">
				<span class="iicon"><Icon name={icon} size={36} color={iconColor(icon)} /></span>
				<div class="iwhen">
					<span class="ititle">{title}</span>
					<div class="inow">
						<span class="itemp">{deg(s.tmp)}</span>
						<span class="iword">{[word, pcp].filter(Boolean).join(' ')}</span>
						{#if s.feels != null && Math.round(s.feels) !== Math.round(s.tmp ?? NaN)}
							<span class="ifeel">체감 {deg(s.feels)}</span>
						{/if}
					</div>
				</div>
				<div class="ichips">
					{#if isNow && tl.air && tl.air.grade > 0}
						<span class="chip"><i class="dot" style="background:{AIR_COLOR[tl.air.grade] ?? 'var(--text-4)'}"></i>{air}</span>
					{/if}
					{#if !isNow}<button type="button" class="pill" onclick={onnow}>지금으로</button>{/if}
				</div>
			</div>
			<div class="tiles">
				<div class="tile">
					<span class="tl">강수확률</span>
					<span class="tv">{s.pop != null ? `${s.pop}%` : '–'}</span>
					<span class="meter"><span style="width:{s.pop ?? 0}%;background:var(--bar-rain)"></span></span>
				</div>
				<div class="tile">
					<span class="tl">{sno ? '적설' : '강수량'}</span>
					<span class="tv">{sno || pcp || (s.wet ? '조금' : '없음')}</span>
					<span class="ts">{s.h === 3 ? '3시간' : '1시간'}</span>
				</div>
				<div class="tile">
					<span class="tl">바람</span>
					<span class="tv wind">
						{#if s.vec != null && (s.wsd == null || s.wsd >= 0.5)}
							<span class="arw" style="transform:rotate({(s.vec + 180) % 360}deg)"
								><Icon name="arrow" size={16} color="var(--text-2)" /></span
							>
						{/if}
						{s.wsd != null ? (s.wsd < 0.5 ? '고요' : `${Math.round(s.wsd)}m/s`) : windText(s) || '–'}
					</span>
					<span class="ts">{s.wsd != null && s.wsd >= 0.5 ? windText(s).split(' ')[0] : ' '}</span>
				</div>
				<div class="tile">
					<span class="tl">습도</span>
					<span class="tv">{s.reh != null ? `${Math.round(s.reh)}%` : '–'}</span>
					<span class="meter"><span style="width:{s.reh ?? 0}%;background:var(--text-4)"></span></span>
				</div>
				<div class="tile">
					<span class="tl">어제 이 시각</span>
					<span class="tv">{yesterday ? deg(yesterday.tmp) : '–'}</span>
					<span class="ts">{yDiff}</span>
				</div>
			</div>
		</section>
	{:else}
		<section class="panel" aria-live="polite" aria-label="선택한 칸 자세히">
			<div class="head">
				<span class="title">{title}</span>
				{#if !isNow}<button type="button" class="pill" onclick={onnow}>지금으로</button>{/if}
			</div>
			<div class="main">
				<span class="t">{deg(s.tmp)}</span>
				<span class="sub"
					>{[s.feels != null ? `체감 ${deg(s.feels)}` : '', word, pcp].filter(Boolean).join(' · ')}</span
				>
			</div>
			<div class="grid">
				{#if popText}<span>{popText}</span>{/if}
				<span>{pcp ? `강수량 ${pcp}` : s.wet ? '강수량 조금' : '강수량 없음'}</span>
				{#if windText(s)}<span>{windText(s)}</span>{/if}
				{#if s.reh != null}<span>습도 {Math.round(s.reh)}%</span>{/if}
				{#if sno}<span>적설 {sno}</span>{/if}
				{#if isNow && air}<span>{air}</span>{/if}
				{#if isNow && yText}<span>{yText}</span>{/if}
			</div>
			{#if !isNow && yText}<div class="note">{yText}</div>{/if}
		</section>
	{/if}
{/if}

<style>
	.panel {
		margin: 8px 16px 0;
		padding: 14px 16px;
		border: 1px solid color-mix(in srgb, var(--sky-bottom, var(--line)) 14%, var(--line));
		border-radius: 18px;
		background: linear-gradient(
			160deg,
			color-mix(in srgb, var(--sky-bottom, var(--surface)) 9%, var(--surface)),
			var(--surface) 70%
		);
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		min-height: 32px;
	}
	.title {
		font-size: 13px;
		color: var(--text-3);
	}
	.main {
		display: flex;
		align-items: baseline;
		gap: 8px;
		flex-wrap: wrap;
	}
	.t {
		font-size: 30px;
		font-weight: 700;
		letter-spacing: -0.5px;
		font-variant-numeric: tabular-nums;
	}
	.sub {
		font-size: 14px;
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 6px 12px;
		font-size: 14px;
		color: var(--text-2);
	}
	.note {
		font-size: 13px;
		color: var(--text-2);
	}
	/* 타임라인의 '지금' 표시와 같은 파란 계열 칩. 보이는 높이는 32px, 누르는 영역은 44px */
	.pill {
		position: relative;
		display: inline-flex;
		align-items: center;
		gap: 4px;
		height: 32px;
		padding: 0 12px 0 10px;
		border: 1px solid var(--accent-border);
		border-radius: 16px;
		background: var(--accent-weak);
		color: var(--accent-text);
		font: inherit;
		font-size: 13px;
		font-weight: 600;
		flex: none;
		cursor: pointer;
		transition: background 0.15s;
	}
	.pill::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--accent);
	}
	.pill::after {
		content: '';
		position: absolute;
		inset: -6px -2px;
	}
	.pill:hover {
		background: color-mix(in srgb, var(--accent-weak), var(--accent) 12%);
	}
	.pill:active {
		background: color-mix(in srgb, var(--accent-weak), var(--accent) 22%);
	}
	/* PC 차트 아래 카드 */
	.inline {
		display: flex;
		flex-direction: column;
		gap: 14px;
		margin: 4px 4px 0;
		padding: 16px;
		border-radius: 18px;
		background: linear-gradient(
			160deg,
			color-mix(in srgb, var(--sky-bottom, var(--surface-2)) 10%, var(--surface-2)),
			var(--surface-2) 75%
		);
	}
	.ihead {
		display: flex;
		align-items: center;
		gap: 14px;
		min-height: 56px;
	}
	.iicon {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 52px;
		height: 52px;
		flex: none;
		border-radius: 16px;
		background: var(--surface);
	}
	.iwhen {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.ititle {
		font-size: 13px;
		font-weight: 600;
		color: var(--text-3);
	}
	.inow {
		display: flex;
		align-items: baseline;
		gap: 10px;
		flex-wrap: wrap;
	}
	.itemp {
		font-size: 30px;
		font-weight: 700;
		line-height: 1.1;
		letter-spacing: -0.5px;
		font-variant-numeric: tabular-nums;
	}
	.iword {
		font-size: 15px;
		font-weight: 600;
	}
	.ifeel {
		font-size: 13px;
		color: var(--text-3);
	}
	.ichips {
		margin-left: auto;
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		justify-content: flex-end;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 30px;
		padding: 0 12px;
		border-radius: 15px;
		background: var(--surface);
		font-size: 13px;
		font-weight: 500;
		color: var(--text-2);
		white-space: nowrap;
	}
	.dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
	}
	.tiles {
		display: grid;
		grid-template-columns: repeat(5, minmax(0, 1fr));
		gap: 8px;
	}
	.tile {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 10px 12px 12px;
		border-radius: 14px;
		background: var(--surface);
		min-width: 0;
	}
	.tl {
		font-size: 12px;
		font-weight: 600;
		color: var(--text-3);
	}
	.tv {
		font-size: 18px;
		font-weight: 700;
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.tv.wind {
		display: flex;
		align-items: center;
		gap: 4px;
	}
	.arw {
		display: inline-flex;
	}
	.ts {
		font-size: 12px;
		color: var(--text-3);
		min-height: 6px;
		white-space: pre;
	}
	.meter {
		height: 4px;
		margin-top: 2px;
		border-radius: 2px;
		background: var(--line);
		overflow: hidden;
	}
	.meter span {
		display: block;
		height: 100%;
		border-radius: 2px;
		transition: width 0.2s;
	}
</style>
