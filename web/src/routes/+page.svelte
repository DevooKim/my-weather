<script lang="ts">
	import { app } from '$lib/app.svelte';
	import { toggleBell } from '$lib/bell';
	import Daily from '$lib/components/Daily.svelte';
	import DetailPanel from '$lib/components/DetailPanel.svelte';
	import Hero from '$lib/components/Hero.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import InstallSheet from '$lib/components/InstallSheet.svelte';
	import LoginSheet from '$lib/components/LoginSheet.svelte';
	import PlaceList from '$lib/components/PlaceList.svelte';
	import Settings from '$lib/components/Settings.svelte';
	import Sheet from '$lib/components/Sheet.svelte';
	import Timeline from '$lib/components/Timeline.svelte';
	import { hm, kst, shortFull, summaryLines } from '$lib/format';
	import { sceneOf } from '$lib/scene';
	import type { Place } from '$lib/types';
	import { onMount, tick } from 'svelte';

	let vh = $state(844);
	let scrollY = $state(0);
	let desktop = $state(false);

	onMount(() => {
		app.init();
		const mq = matchMedia('(min-width: 960px)');
		const upd = () => {
			desktop = mq.matches;
			vh = window.innerHeight;
		};
		upd();
		mq.addEventListener('change', upd);
		window.addEventListener('resize', upd);
		// 스크롤 이벤트마다가 아니라 화면을 그릴 때 한 번만 머리 높이를 갱신한다
		let sraf = 0;
		const onScroll = () => {
			if (sraf) return;
			sraf = requestAnimationFrame(() => {
				sraf = 0;
				scrollY = Math.max(0, window.scrollY);
			});
		};
		window.addEventListener('scroll', onScroll, { passive: true });
		const onPop = () => {
			if (app.settingsOpen) app.settingsOpen = false;
		};
		window.addEventListener('popstate', onPop);
		return () => {
			mq.removeEventListener('change', upd);
			window.removeEventListener('resize', upd);
			window.removeEventListener('scroll', onScroll);
			if (sraf) cancelAnimationFrame(sraf);
			window.removeEventListener('popstate', onPop);
		};
	});

	// 머리 높이: 1A → 1C → 한 줄
	const H1A = $derived(Math.round(Math.min(460, Math.max(340, vh * 0.51))));
	const H1C = 172;
	const BAR = 56;
	const heroH = $derived(Math.max(BAR, H1A - scrollY));

	const scene = $derived(sceneOf(app.timeline));
	$effect(() => {
		document.querySelector('meta[name="theme-color"]')?.setAttribute('content', scene.top);
	});

	// 선택 칸: null = 지금
	let selT = $state<string | null>(null);
	let hoverIdx = $state<number | null>(null);
	const tl = $derived(app.timeline);
	const nowIdx = $derived(tl ? Math.max(0, tl.slots.findIndex((s) => s.phase === 'now')) : 0);
	const selIdx = $derived.by(() => {
		if (!tl) return 0;
		if (selT) {
			const i = tl.slots.findIndex((s) => s.t === selT);
			if (i >= 0) return i;
		}
		return nowIdx;
	});
	const shownIdx = $derived(hoverIdx ?? selIdx);
	let timeline: Timeline | undefined = $state();

	function selectSlot(i: number) {
		if (!tl) return;
		selT = tl.slots[i].phase === 'now' ? null : tl.slots[i].t;
	}
	function toNow() {
		selT = null;
		timeline?.scrollToIndex(nowIdx);
	}

	async function pickDay(date: string) {
		if (!tl) return;
		let i = tl.slots.findIndex((s) => kst(s.t).date === date && kst(s.t).h >= 6);
		if (i < 0) i = tl.slots.findIndex((s) => kst(s.t).date === date);
		if (i < 0) return;
		selectSlot(i);
		if (!desktop) {
			const smooth = !matchMedia('(prefers-reduced-motion: reduce)').matches;
			window.scrollTo({ top: H1A - H1C, behavior: smooth ? 'smooth' : 'auto' });
		}
		await tick();
		timeline?.scrollToIndex(i, 0.15);
	}

	function pickPlace(p: Place) {
		app.sheetOpen = false;
		selT = null;
		app.select(app.favOf(p) ?? p);
		window.scrollTo({ top: 0 });
	}

	// 메뉴 버튼: 짧은 시간 안에 3번 → 로그인. 한 번 누른 동작은 0.4초 뒤에 실행.
	let taps = 0;
	let tapTimer: ReturnType<typeof setTimeout> | null = null;
	function menu() {
		taps++;
		if (tapTimer) clearTimeout(tapTimer);
		if (taps >= 3) {
			taps = 0;
			if (app.admin) openSettings();
			else app.loginOpen = true;
			return;
		}
		tapTimer = setTimeout(() => {
			taps = 0;
			openSettings();
		}, 400);
	}
	function openSettings() {
		app.settingsOpen = true;
		history.pushState({ settings: true }, '');
	}
	function closeSettings() {
		if (history.state?.settings) history.back();
		else app.settingsOpen = false;
	}

	const staleText = $derived(app.staleAt ? hm(new Date(app.staleAt).toISOString()) : '');
	const pending = $derived(tl && !tl.complete);
	const lines = $derived(summaryLines(tl?.summary.text));
	const placeTitle = $derived.by(() => {
		const p = app.place;
		if (!p) return '';
		const sf = shortFull(p.full, '');
		return !sf || sf.endsWith(p.name) ? sf || p.name : `${p.name} · ${sf}`;
	});
	let retryLeft = $state(0);
	$effect(() => {
		const e = app.error;
		if (e?.kind !== 'unavailable') return;
		const upd = () => (retryLeft = Math.max(0, Math.round((e.retryAt - Date.now()) / 1000)));
		upd();
		const t = setInterval(upd, 1000);
		return () => clearInterval(t);
	});
</script>

<svelte:head>
	<title>{app.place ? `${app.place.name} 날씨 · my-weather` : 'my-weather'}</title>
</svelte:head>

{#snippet states()}
	{#if app.error?.kind === 'network' && tl}
		<div class="banner" role="status">
			<span>연결이 끊겨 {staleText}에 받은 날씨를 보여 드려요</span>
			<button type="button" onclick={() => app.reload()}>다시 시도</button>
		</div>
	{:else if app.error && !tl}
		<div class="empty" role="status">
			<Icon name="cloud" size={36} color="var(--text-3)" />
			{#if app.error.kind === 'unavailable'}
				<b>이 지역 예보를 아직 받지 못했어요</b>
				<span
					>기상청 응답이 늦어지고 있어요. {retryLeft > 0
						? `${retryLeft >= 60 ? '1분' : `${retryLeft}초`} 뒤 자동으로 다시 시도합니다.`
						: '다시 시도하는 중이에요.'}</span
				>
			{:else if app.error.kind === 'network'}
				<b>서버에 연결할 수 없어요</b>
				<span>인터넷 연결을 확인해 주세요. 1분 뒤 자동으로 다시 시도합니다.</span>
				<button type="button" class="pill" onclick={() => app.reload()}>다시 시도</button>
			{:else}
				<b>날씨를 불러오지 못했어요</b>
				<span>{app.error.message}</span>
				<button type="button" class="pill" onclick={() => app.reload()}>다시 시도</button>
			{/if}
		</div>
	{:else if !tl && app.place}
		<div class="empty" role="status" aria-busy="true"><span>불러오는 중…</span></div>
	{:else if !app.place}
		<div class="empty">
			<b>장소를 골라 주세요</b>
			<span>현재 위치를 쓰거나 동 이름으로 찾을 수 있어요.</span>
			<button
				type="button"
				class="pill"
				onclick={() =>
					desktop ? document.querySelector<HTMLInputElement>('aside input')?.focus() : (app.sheetOpen = true)}
				>장소 고르기</button
			>
		</div>
	{/if}
{/snippet}

{#snippet pendingNote()}
	{#if pending}
		<p class="pending" role="status">지난 기록을 채우는 중이에요. 잠시 뒤 저절로 바뀌어요.</p>
	{/if}
{/snippet}

{#if desktop}
	<div class="desk">
		<div class="deskbar">
			<span class="brand">my-weather</span>
			<button type="button" class="menu" aria-label="메뉴" onclick={menu}><Icon name="menu" size={22} /></button>
		</div>
		<div class="cols">
			<aside>
				<PlaceList variant="aside" onpick={pickPlace} onbell={toggleBell} />
			</aside>
			<main>
				{#if app.place}
					<div class="mhead">
						<h1>{placeTitle}</h1>
						{#if lines.length}<p>{lines.join(' · ')}</p>{/if}
					</div>
				{/if}
				{@render states()}
				{#if tl}
					<Timeline
						bind:this={timeline}
						{tl}
						selected={selIdx}
						meteo={true}
						cw={40}
						onselect={selectSlot}
						onhover={(i) => (hoverIdx = i)}
					/>
					<DetailPanel {tl} idx={shownIdx} onnow={toNow} inline={true} />
					{@render pendingNote()}
					<Daily {tl} onpick={pickDay} />
				{/if}
			</main>
		</div>
	</div>
{:else}
	<Hero
		h={heroH}
		full={H1A}
		compact={H1C}
		bar={BAR}
		{scene}
		onplace={() => (app.sheetOpen = true)}
		onmenu={menu}
	/>
	<!-- min-height: 내용이 짧아도 머리가 한 줄이 될 만큼 내릴 수 있게 -->
	<div class="phone" style="padding-top:{H1A}px; min-height:{H1A + vh - BAR}px">
		{@render states()}
		{#if tl}
			<section class="tl">
				<Timeline bind:this={timeline} {tl} selected={selIdx} onselect={selectSlot} />
			</section>
			<DetailPanel {tl} idx={selIdx} onnow={toNow} />
			{@render pendingNote()}
			<Daily {tl} onpick={pickDay} />
		{/if}
		<p class="foot">기상청 단기예보 조회서비스</p>
	</div>
{/if}

{#if app.sheetOpen && !desktop}
	<Sheet label="장소 선택" closable={!app.sheetForced} tall onclose={() => (app.sheetOpen = false)}>
		<PlaceList onpick={pickPlace} onbell={toggleBell} />
	</Sheet>
{/if}
{#if app.loginOpen}<LoginSheet />{/if}
{#if app.installOpen}<InstallSheet />{/if}
{#if app.settingsOpen}<Settings onclose={closeSettings} />{/if}
{#if app.toast}
	<div class="toast" role="status" aria-live="polite">{app.toast}</div>
{/if}

<style>
	.phone {
		max-width: 720px;
		margin: 0 auto;
		background: var(--page);
		min-height: 100vh;
	}
	.tl {
		padding: 14px 0 6px;
	}
	.foot {
		margin: 24px 20px 8px;
		font-size: 12px;
		color: var(--text-3);
	}
	.pending {
		margin: 8px 20px 0;
		font-size: 12px;
		color: var(--text-3);
	}
	.banner {
		display: flex;
		align-items: center;
		gap: 10px;
		margin: 12px 16px 0;
		padding: 10px 12px;
		background: var(--warn-bg);
		border-radius: 10px;
		font-size: 14px;
		color: var(--warn-text);
	}
	.banner span {
		flex-grow: 1;
	}
	.banner button {
		min-height: 44px;
		padding: 0 14px;
		border: 1px solid var(--warn-border);
		border-radius: 22px;
		background: var(--surface);
		font: inherit;
		font-size: 13px;
		color: var(--warn-text);
		flex: none;
	}
	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
		margin: 16px;
		padding: 24px 16px;
		background: var(--surface-2);
		border-radius: 12px;
		text-align: center;
		font-size: 13px;
		color: var(--text-2);
	}
	.empty b {
		font-size: 15px;
		color: var(--text);
	}
	.pill {
		min-height: 44px;
		padding: 0 16px;
		border: 0;
		border-radius: 22px;
		background: var(--btn-dark);
		color: var(--btn-dark-text);
		font: inherit;
		font-size: 13px;
	}
	.toast {
		position: fixed;
		left: 50%;
		bottom: calc(24px + env(safe-area-inset-bottom));
		transform: translateX(-50%);
		z-index: 60;
		max-width: calc(100% - 32px);
		padding: 12px 16px;
		border-radius: 12px;
		background: var(--btn-dark);
		color: var(--btn-dark-text);
		font-size: 14px;
		box-shadow: 0 6px 18px rgba(0, 0, 0, 0.2);
	}

	/* 4B 데스크톱 2단 */
	.desk {
		min-height: 100vh;
		background: var(--page-desk);
		padding: 0 16px 24px;
	}
	.deskbar {
		max-width: 1240px;
		margin: 0 auto;
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 0;
	}
	.brand {
		font-size: 15px;
		font-weight: 700;
		color: var(--text-2);
	}
	.menu {
		width: 44px;
		height: 44px;
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: 0;
		color: var(--text);
	}
	.cols {
		max-width: 1240px;
		margin: 0 auto;
		display: flex;
		gap: 16px;
		align-items: flex-start;
	}
	aside {
		flex: 0 0 300px;
		position: sticky;
		top: 16px;
		max-height: calc(100vh - 32px);
		overflow-y: auto;
		padding: 2px;
	}
	main {
		flex: 1 1 560px;
		min-width: 0;
		background: var(--surface);
		border-radius: 12px;
		padding: 16px 12px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.mhead {
		padding: 0 8px;
	}
	.mhead h1 {
		margin: 0;
		font-size: 18px;
	}
	.mhead p {
		margin: 4px 0 0;
		font-size: 14px;
		color: var(--text-2);
	}
	main :global(.h) {
		margin-left: 8px;
	}
	main :global(.list) {
		margin: 0 4px;
	}
</style>
