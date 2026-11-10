<script lang="ts">
	import { airText, amountText, dayLabel, deg, kst, visText, weatherWord, windText } from '$lib/format';
	import type { Timeline } from '$lib/types';

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
	const vis = $derived(visText(tl.fog));
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
	const popText = $derived(
		s?.pop != null ? `강수확률 ${s.pop}%` : ''
	);
</script>

{#if s}
	{#if inline}
		<div class="inline" aria-live="polite">
			<b>{title}</b>
			<span>{deg(s.tmp)}{s.feels != null && Math.round(s.feels) !== Math.round(s.tmp ?? NaN) ? ` 체감 ${deg(s.feels)}` : ''}</span>
			{#if word || pcp}<span>{[word, pcp].filter(Boolean).join(' ')}{s.pop != null ? ` · 강수 ${s.pop}%` : ''}</span>{/if}
			{#if windText(s)}<span>{windText(s)}</span>{/if}
			{#if s.reh != null}<span>습도 {Math.round(s.reh)}%</span>{/if}
			{#if sno}<span>적설 {sno}</span>{/if}
			{#if isNow && air}<span>{air}</span>{/if}
			{#if isNow && vis}<span>{vis}</span>{/if}
			{#if yText}<span class="muted">{yText}</span>{/if}
			{#if !isNow}<button type="button" class="pill" onclick={onnow}>지금으로</button>{/if}
		</div>
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
				{#if isNow && vis}<span>{vis}</span>{/if}
				{#if isNow && yText}<span>{yText}</span>{/if}
			</div>
			{#if !isNow && yText}<div class="note">{yText}</div>{/if}
		</section>
	{/if}
{/if}

<style>
	.panel {
		margin: 6px 16px 0;
		padding: 12px 14px;
		border: 1px solid var(--line-2);
		border-radius: 12px;
		background: var(--surface);
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
		font-size: 26px;
		font-weight: 700;
	}
	.sub {
		font-size: 14px;
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 4px 12px;
		font-size: 14px;
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
	.inline {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px 20px;
		padding: 10px 12px;
		border-top: 1px solid var(--line);
		font-size: 14px;
	}
	.inline .pill {
		margin-left: auto;
	}
	.muted {
		color: var(--text-2);
	}
</style>
