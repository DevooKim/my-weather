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
	import Motion from '$lib/components/Motion.svelte';
	import Sheet from '$lib/components/Sheet.svelte';
	import Timeline from '$lib/components/Timeline.svelte';
	import WeatherCanvas from '$lib/components/WeatherCanvas.svelte';
	import { deg, hm, kst, shortFull, summaryLines } from '$lib/format';
	import { heroText } from '$lib/hero';
	import { sceneOf } from '$lib/scene';
	import type { Place } from '$lib/types';
	import { onMount, tick } from 'svelte';

	let vh = $state(844);
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
		// 설정·날씨 모션은 history 항목({settings, motion})으로 열린다. 뒤로 가면 그 항목에 맞춰 닫는다.
		const onPop = () => {
			app.settingsOpen = !!history.state?.settings;
			app.motionOpen = !!history.state?.motion;
		};
		window.addEventListener('popstate', onPop);
		return () => {
			mq.removeEventListener('change', upd);
			window.removeEventListener('resize', upd);
			window.removeEventListener('popstate', onPop);
		};
	});

	// 머리 높이: 1A → 1C → 한 줄
	const H1A = $derived(Math.round(Math.min(460, Math.max(340, vh * 0.51))));
	const H1C = 172;
	const BAR = 56;

	const scene = $derived(app.demo?.scene ?? sceneOf(app.timeline));
	$effect(() => {
		document.querySelector('meta[name="theme-color"]')?.setAttribute('content', scene.top);
		// 아래 차트·카드가 지금 하늘색을 옅게 이어받는다
		document.documentElement.style.setProperty('--sky-top', scene.top);
		document.documentElement.style.setProperty('--sky-bottom', scene.bottom);
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

	// 메뉴 버튼: 바로 메뉴(설정). 로그인은 메뉴의 정보 칸을 3번 눌러서(Settings)
	function menu() {
		openSettings();
	}
	function openSettings() {
		app.settingsOpen = true;
		history.pushState({ settings: true }, '');
	}
	function closeMotion() {
		if (history.state?.motion) history.back();
		else app.motionOpen = false;
	}
	function closeSettings() {
		if (history.state?.settings) history.back();
		else app.settingsOpen = false;
	}

	const staleText = $derived(app.staleAt ? hm(new Date(app.staleAt).toISOString()) : '');
	const pending = $derived(tl && !tl.complete);
	const lines = $derived(summaryLines(tl?.summary.text));
	const ht = $derived(heroText(tl));
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
	<!-- PC: 날씨 모션이 창 전체 배경. 위쪽 넓은 하늘에 지금 날씨, 그 아래 카드가 하늘 위로 올라온다 -->
	<div class="desk">
		<div class="sky" aria-hidden="true"><WeatherCanvas {scene} maxDpr={1} /></div>
		<div class="deskbar">
			<span class="brand">my-weather</span>
			<button type="button" class="menu" aria-label="메뉴" onclick={menu}><Icon name="menu" size={22} /></button>
		</div>
		<section class="dhero" aria-label="지금 날씨">
			{#if app.place}<h1>{placeTitle}</h1>{/if}
			{#if tl}
				<div class="dnow">
					<span class="dtemp">{deg(ht.tmp)}</span>
					<span class="ddesc">{ht.desc}</span>
				</div>
				{#if lines.length}
					<div class="dsum">{#each lines as l, i (i)}<p>{l}</p>{/each}</div>
				{/if}
			{/if}
		</section>
		<div class="cols">
			<aside>
				<PlaceList variant="aside" onpick={pickPlace} onbell={toggleBell} />
			</aside>
			<main>
				{@render states()}
				{#if tl}
					<Timeline
						bind:this={timeline}
						{tl}
						selected={selIdx}
						meteo={true}
						cw={46}
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
		full={H1A}
		bar={BAR}
		{scene}
		onplace={() => (app.sheetOpen = true)}
		onmenu={menu}
	/>
	<!-- min-height: 내용이 짧아도 머리가 한 줄이 될 만큼 내릴 수 있게 -->
	<div class="phone" style="min-height:{vh - BAR}px">
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
{#if app.motionOpen}<Motion onclose={closeMotion} />{/if}
{#if app.demo}
	<div class="demo" role="status">
		<span>모션 데모 · {app.demo.name}</span>
		<button type="button" onclick={() => (app.demo = null)}>끄기</button>
	</div>
{/if}
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
	/* 머리(모션) 바로 아래: 하늘 아랫색이 옅게 번져 내려와 흰 바탕과 이어진다 */
	.tl {
		padding: 14px 0 6px;
		background: linear-gradient(
			to bottom,
			color-mix(in srgb, var(--sky-bottom, var(--page)) 12%, var(--page)),
			var(--page) 85%
		);
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
	.demo {
		position: fixed;
		left: 50%;
		bottom: calc(24px + env(safe-area-inset-bottom));
		transform: translateX(-50%);
		z-index: 55;
		display: flex;
		align-items: center;
		gap: 12px;
		max-width: calc(100% - 32px);
		padding: 6px 6px 6px 16px;
		border-radius: 24px;
		background: var(--btn-dark);
		color: var(--btn-dark-text);
		font-size: 14px;
		white-space: nowrap;
		box-shadow: 0 6px 18px rgba(0, 0, 0, 0.25);
	}
	.demo button {
		min-height: 36px;
		padding: 0 14px;
		border: 0;
		border-radius: 18px;
		background: rgba(255, 255, 255, 0.16);
		color: inherit;
		font: inherit;
		font-weight: 600;
		cursor: pointer;
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

	/* 4B 데스크톱: 모션 배경 + 하늘 머리 + 2단 카드 */
	.desk {
		position: relative;
		min-height: 100vh;
		padding: 0 24px 32px;
		isolation: isolate;
	}
	.sky {
		position: fixed;
		inset: 0;
		z-index: -1;
	}
	.deskbar,
	.dhero,
	.cols {
		max-width: 1240px;
		margin: 0 auto;
	}
	.deskbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 0;
		color: #fff;
	}
	.brand {
		font-size: 15px;
		font-weight: 700;
		color: rgba(255, 255, 255, 0.85);
	}
	.menu {
		width: 44px;
		height: 44px;
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: 0;
		color: #fff;
		border-radius: 22px;
	}
	.menu:hover {
		background: rgba(255, 255, 255, 0.12);
	}
	/* 하늘 머리: 모션 위 글자라 원래 글꼴. 창 높이에 맞춰 하늘이 넉넉히 보이게 */
	.dhero {
		min-height: clamp(260px, 42vh, 460px);
		display: flex;
		flex-direction: column;
		justify-content: flex-end;
		gap: 6px;
		padding: 0 4px 28px;
		color: #fff;
		font-family: var(--font-motion);
		text-shadow: 0 1px 12px rgba(0, 0, 0, 0.18);
	}
	.dhero h1 {
		margin: 0;
		font-size: 18px;
		font-weight: 500;
		opacity: 0.92;
	}
	.dnow {
		display: flex;
		align-items: baseline;
		gap: 16px;
		flex-wrap: wrap;
	}
	.dtemp {
		font-size: 112px;
		font-weight: 700;
		line-height: 1;
		letter-spacing: -3px;
	}
	.ddesc {
		font-size: 22px;
		font-weight: 500;
	}
	.dsum {
		display: flex;
		flex-direction: column;
		gap: 4px;
		font-size: 16px;
		opacity: 0.92;
	}
	.dsum p {
		margin: 0;
	}
	.cols {
		display: flex;
		gap: 16px;
		align-items: flex-start;
	}
	aside,
	main {
		background: var(--surface);
		border-radius: 20px;
		box-shadow: 0 12px 40px rgba(0, 0, 0, 0.18);
	}
	aside {
		flex: 0 0 300px;
		position: sticky;
		top: 16px;
		max-height: calc(100vh - 32px);
		overflow-y: auto;
		padding: 12px 10px;
	}
	main {
		flex: 1 1 560px;
		min-width: 0;
		padding: 16px 12px;
		display: flex;
		flex-direction: column;
		gap: 12px;
		overflow: hidden;
	}
	main :global(.h) {
		margin-left: 8px;
	}
	main :global(.list) {
		margin: 0 4px;
	}
</style>
