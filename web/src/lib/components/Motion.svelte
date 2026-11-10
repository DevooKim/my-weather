<script lang="ts">
	// 날씨 모션 보기: 메뉴(설정) 위에 뜨는 화면. 날씨·시간대별 배경 모션을 한눈에 본다.
	import { MOTION_DEMOS } from '$lib/motion-demo';
	import { onMount } from 'svelte';
	import Icon from './Icon.svelte';
	import WeatherCanvas from './WeatherCanvas.svelte';

	let { onclose }: { onclose: () => void } = $props();
	onMount(() => document.querySelector<HTMLElement>('.motion h1')?.focus());
</script>

<div class="motion" role="dialog" aria-modal="true" aria-label="날씨 모션">
	<header>
		<button type="button" class="icon-btn" aria-label="뒤로" onclick={onclose}><Icon name="back" size={22} /></button>
		<h1 tabindex="-1">날씨 모션</h1>
	</header>
	<ul class="grid">
		{#each MOTION_DEMOS as d (d.name)}
			<li class="cardm">
				<WeatherCanvas scene={d.scene} />
				<span class="label">{d.name}</span>
			</li>
		{/each}
	</ul>
</div>

<style>
	.motion {
		position: fixed;
		inset: 0;
		z-index: 41;
		background: var(--page-2);
		color: var(--text);
		overflow-y: auto;
	}
	header {
		position: sticky;
		top: 0;
		z-index: 1;
		display: flex;
		align-items: center;
		gap: 4px;
		padding: calc(6px + env(safe-area-inset-top)) 8px 6px;
		background: var(--surface);
		border-bottom: 1px solid var(--line-2);
	}
	h1 {
		margin: 0;
		font-size: 17px;
		outline: none;
	}
	.icon-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 44px;
		height: 44px;
		border: 0;
		background: none;
		color: var(--text);
		border-radius: 22px;
		cursor: pointer;
	}
	.grid {
		list-style: none;
		margin: 0 auto;
		max-width: 1200px;
		padding: 16px 16px calc(24px + env(safe-area-inset-bottom));
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
		gap: 12px;
	}
	.cardm {
		position: relative;
		height: 220px;
		border-radius: 12px;
		overflow: hidden;
	}
	.label {
		position: absolute;
		left: 10px;
		right: 10px;
		bottom: 10px;
		font-size: 13px;
		color: #fff;
		text-shadow: 0 1px 2px rgba(0, 0, 0, 0.6);
	}
</style>
