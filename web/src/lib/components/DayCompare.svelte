<script lang="ts">
	// 어제·오늘·내일·모레를 00시부터 3시간 간격으로 한 표에. 세로로 같은 시각이 줄을 맞춰 비교가 쉽다.
	// 칸 바탕은 기온 색(차트·일별 막대와 같은 눈금)을 옅게 깔고, 누르면 타임라인에서 그 칸을 고른다.
	import { kst, round1 } from '$lib/format';
	import type { Timeline } from '$lib/types';
	import { iconColor, slotIcon, tempColor } from '$lib/weather';
	import Icon from './Icon.svelte';

	let { tl, selected, onpick }: { tl: Timeline; selected: number; onpick: (i: number) => void } = $props();

	// 풍속: m/s 정수(0.5 미만은 0). 값이 없고 강약 코드만 있는 먼 칸은 약·중·강
	const windShort = (s: Timeline['slots'][number]) =>
		s.wsd != null ? (s.wsd < 0.5 ? '–' : `${Math.round(s.wsd)}`) : s.wsd_code ? (['', '약', '중', '강'][s.wsd_code] ?? '') : '';

	// 강수량: 그 칸의 mm(1시간 칸은 1시간, 3시간 칸은 3시간 합). 먼 칸처럼 강약 코드만 있으면 약·중·강
	const rainShort = (s: Timeline['slots'][number]) => {
		if (!s.wet || !s.pcp) return '';
		if (s.pcp.code != null) return ['', '약', '중', '강'][s.pcp.code] ?? '';
		return s.pcp.mm > 0 ? String(round1(s.pcp.mm)) : '';
	};

	const HOURS = [0, 3, 6, 9, 12, 15, 18, 21];
	const DAY = 86400_000;

	const nowMs = $derived(new Date(tl.now).getTime());
	const nowH = $derived(kst(nowMs).h);

	// 그 시각을 담은 칸(1시간 칸이면 정각 칸, 3시간 칸이면 그 구간)
	function slotAt(ms: number) {
		return tl.slots.findIndex((x) => {
			const t0 = new Date(x.t).getTime();
			return t0 <= ms && ms < t0 + (x.h || 1) * 3600_000;
		});
	}

	const rows = $derived(
		[-1, 0, 1, 2].map((off) => {
			const p = kst(nowMs + off * DAY);
			return {
				off,
				name: ['어제', '오늘', '내일', '모레'][off + 1],
				sub: `${p.mo}/${p.d}`,
				cells: HOURS.map((h) => {
					const ms = Date.parse(`${p.date}T${String(h).padStart(2, '0')}:00:00+09:00`);
					const i = slotAt(ms);
					const s = i >= 0 ? tl.slots[i] : null;
					return {
						h,
						i,
						s,
						icon: s ? slotIcon(s, tl.daily) : null,
						past: ms + 3 * 3600_000 <= nowMs,
						now: off === 0 && h <= nowH && nowH < h + 3
					};
				})
			};
		})
	);
</script>

<section class="cmp" aria-label="어제부터 모레까지 같은 시각 비교">
	<h2 class="h">같은 시각 비교</h2>
	<div class="grid" role="table">
		<div class="row head" role="row">
			<span class="lab" role="columnheader"></span>
			{#each HOURS as h (h)}
				<span class="hh" class:nowh={h <= nowH && nowH < h + 3} role="columnheader">{String(h).padStart(2, '0')}</span>
			{/each}
		</div>
		{#each rows as r (r.off)}
			<div class="row" class:today={r.off === 0} role="row">
				<span class="lab" role="rowheader"><b>{r.name}</b><small>{r.sub}</small></span>
				{#each r.cells as c (c.h)}
					{#if c.s && c.s.tmp != null}
						<button
							type="button"
							role="cell"
							class="cell"
							class:past={c.past}
							class:now={c.now}
							class:sel={c.i === selected}
							style="--tc:{tempColor(c.s.tmp)}"
							aria-label="{r.name} {c.h}시 {Math.round(c.s.tmp)}도{rainShort(c.s)
								? ` 강수량 ${rainShort(c.s)}${c.s.pcp?.code != null ? '' : 'mm'}`
								: ''}{c.s.pop ? ` 강수확률 ${c.s.pop}%` : ''}{c.s.wsd != null ? ` 풍속 ${Math.round(c.s.wsd)}m/s` : ''}"
							onclick={() => onpick(c.i)}
						>
							{#if c.icon}<Icon name={c.icon} size={20} color={iconColor(c.icon)} />{/if}
							<!-- 왼쪽에 보이지 않는 °를 하나 더 두어 숫자가 아이콘과 같은 가운데에 선다 -->
							<span class="t"><i class="ghost">°</i>{Math.round(c.s.tmp)}°</span>
							<span class="rp"
								>{#if rainShort(c.s)}<span class="p"
										>{rainShort(c.s)}{#if c.s.pcp?.code == null}<small>mm</small>{/if}</span
									>{/if}{#if rainShort(c.s) && c.s.pop}<span class="sep">/</span>{/if}{#if c.s.pop}<span
										class="pp">{c.s.pop}<small>%</small></span
									>{/if}</span
							>
							<span class="w">
								{#if c.s.vec != null && (c.s.wsd == null || c.s.wsd >= 0.5)}
									<span class="arw" style="transform:rotate({(c.s.vec + 180) % 360}deg)"
										><Icon name="arrow" size={10} color="var(--text-3)" /></span
									>
								{/if}{windShort(c.s)}
							</span>
						</button>
					{:else}
						<span class="cell empty" role="cell">–</span>
					{/if}
				{/each}
			</div>
		{/each}
	</div>
</section>

<style>
	.h {
		margin: 20px 20px 8px;
		font-size: 15px;
		font-weight: 700;
	}
	.grid {
		margin: 0 12px 0 16px;
		display: flex;
		flex-direction: column;
		gap: 3px;
	}
	.row {
		display: grid;
		grid-template-columns: 32px repeat(8, minmax(0, 1fr));
		gap: 2px;
		align-items: stretch;
	}
	.head {
		margin-bottom: 2px;
	}
	.hh {
		text-align: center;
		font-size: 12px;
		font-weight: 500;
		color: var(--text-3);
		font-variant-numeric: tabular-nums;
	}
	.hh.nowh {
		color: var(--accent-text);
		font-weight: 700;
	}
	.lab {
		display: flex;
		flex-direction: column;
		justify-content: center;
		gap: 1px;
		font-size: 13px;
		color: var(--text-2);
	}
	.lab small {
		font-size: 11px;
		color: var(--text-4);
	}
	.today .lab b {
		color: var(--text);
	}
	.cell {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 1px;
		min-height: 80px;
		padding: 6px 0 4px;
		border: 0;
		border-radius: 12px;
		background: color-mix(in srgb, var(--tc) 16%, var(--surface));
		color: var(--text);
		font: inherit;
		cursor: pointer;
		transition: background 0.15s;
	}
	.cell:hover {
		background: color-mix(in srgb, var(--tc) 26%, var(--surface));
	}
	.cell.past {
		opacity: 0.6;
	}
	.cell.now {
		box-shadow: inset 0 0 0 2px var(--accent);
		opacity: 1;
	}
	.cell.sel {
		box-shadow: inset 0 0 0 2px var(--text-2);
		opacity: 1;
	}
	.cell.now.sel {
		box-shadow: inset 0 0 0 2px var(--accent);
	}
	.t {
		font-size: 15px;
		font-weight: 700;
		font-variant-numeric: tabular-nums;
	}
	/* 강수량 / 강수확률을 한 줄에. 칸이 40px 안팎이라 글자를 조금 줄이고 단위는 더 작게 */
	.rp {
		display: flex;
		align-items: baseline;
		justify-content: center;
		min-height: 13px;
		max-width: 100%;
		font-size: 10px;
		font-weight: 600;
		letter-spacing: -0.2px;
		white-space: nowrap;
		font-variant-numeric: tabular-nums;
		color: var(--accent-text);
	}
	.rp small {
		font-size: 7.5px;
		font-weight: 500;
	}
	/* 폰: 칸이 40px 안팎이라 "0.5mm / 60%"가 한 줄에 들어가지 않는다. 두 줄로 나누고(단위로 구분) 구분자는 숨긴다 */
	@media (max-width: 600px) {
		.rp {
			flex-direction: column;
			align-items: center;
			min-height: 26px;
			font-size: 10px;
		}
		.sep {
			display: none;
		}
	}
	.sep {
		margin: 0 2px;
		color: var(--text-4);
		font-weight: 400;
	}
	.w {
		display: flex;
		align-items: center;
		gap: 1px;
		min-height: 13px;
		font-size: 11px;
		font-weight: 500;
		color: var(--text-3);
		font-variant-numeric: tabular-nums;
	}
	.arw {
		display: inline-flex;
	}
	.ghost {
		visibility: hidden;
		font-style: normal;
	}
	.empty {
		cursor: default;
		background: var(--surface-2);
		color: var(--text-4);
	}
</style>
