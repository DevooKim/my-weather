<script lang="ts">
	import { dailyWord, dayLabel, deg } from '$lib/format';
	import type { Timeline } from '$lib/types';
	import { iconColor, iconFor } from '$lib/weather';
	import Icon from './Icon.svelte';

	let { tl, onpick }: { tl: Timeline; onpick: (date: string) => void } = $props();

	const days = $derived(tl.daily ?? []);
	const lo = $derived(Math.floor(Math.min(...days.map((d) => d.tmin ?? Infinity))));
	const hi = $derived(Math.ceil(Math.max(...days.map((d) => d.tmax ?? -Infinity))));
	const pct = (v: number) => ((v - lo) / Math.max(1, hi - lo)) * 100;
</script>

{#if days.length}
	<h2 class="h">일별</h2>
	<ul class="list">
		{#each days as d (d.date)}
			{@const icon = iconFor(d, true)}
			{@const label = dayLabel(d.date + 'T12:00:00+09:00', tl.now)}
			{@const today = d.label === '오늘'}
			<li>
				<button
					type="button"
					class="row"
					class:today
					onclick={() => onpick(d.date)}
					aria-label="{label} {dailyWord(d)} 최저 {deg(d.tmin)} 최고 {deg(d.tmax)}{d.pop_max != null
						? ` 강수확률 ${d.pop_max}%`
						: ''}. 타임라인에서 보기"
				>
					<span class="label">{label}{d.estimated ? ' (추정)' : ''}</span>
					<span class="icon"><Icon name={icon} size={22} color={iconColor(icon)} /></span>
					<span class="pop">{d.pop_max != null && d.pop_max > 0 ? `${d.pop_max}%` : ''}</span>
					<span class="min">{deg(d.tmin)}</span>
					<span class="bar">
						{#if d.tmin != null && d.tmax != null}
							<span class="fill" style="left:{pct(d.tmin)}%;width:{Math.max(2, pct(d.tmax) - pct(d.tmin))}%"></span>
						{/if}
					</span>
					<span class="max">{deg(d.tmax)}</span>
				</button>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.h {
		margin: 20px 20px 8px;
		font-size: 15px;
		font-weight: 700;
	}
	.list {
		list-style: none;
		margin: 0 16px;
		padding: 0;
	}
	.row {
		width: 100%;
		min-height: 52px;
		padding: 0 4px;
		display: flex;
		align-items: center;
		gap: 8px;
		background: none;
		border: 0;
		border-top: 1px solid var(--line);
		font: inherit;
		color: var(--text);
		text-align: left;
		cursor: pointer;
	}
	.row.today {
		background: var(--accent-weak);
		border-radius: 8px;
	}
	.label {
		width: 76px;
		font-size: 14px;
		color: var(--text-2);
		flex: none;
	}
	.today .label {
		font-weight: 700;
		color: var(--text);
	}
	.icon {
		width: 28px;
		display: flex;
		justify-content: center;
		flex: none;
	}
	.pop {
		width: 40px;
		font-size: 12px;
		color: var(--accent-text);
		text-align: right;
		flex: none;
	}
	.min {
		width: 32px;
		font-size: 14px;
		color: var(--text-3);
		text-align: right;
		flex: none;
	}
	.max {
		width: 32px;
		font-size: 14px;
		font-weight: 500;
		flex: none;
	}
	.bar {
		flex-grow: 1;
		position: relative;
		height: 6px;
		border-radius: 3px;
		background: var(--line);
	}
	.fill {
		position: absolute;
		top: 0;
		bottom: 0;
		border-radius: 3px;
		background: var(--temp-bar);
	}
</style>
