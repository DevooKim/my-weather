<script lang="ts">
	import { api, ApiError, type AlertSettings } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import { hm, kst } from '$lib/format';
	import { ensureSubscribed, needsInstall } from '$lib/push';
	import { onMount } from 'svelte';
	import Icon from './Icon.svelte';

	let { onclose }: { onclose: () => void } = $props();

	let info = $state<any>(null);
	let alerts = $state<AlertSettings | null>(null);
	let alertsErr = $state('');
	let testing = $state(false);

	async function load() {
		api.settingsInfo().then((r) => (info = r)).catch(() => (info = null));
		if (app.admin) {
			try {
				alerts = await api.alertSettings();
				alertsErr = '';
			} catch (e) {
				alertsErr = e instanceof ApiError ? e.message : '알림 설정을 불러오지 못했어요.';
			}
		}
	}
	onMount(() => {
		load();
		const h = document.querySelector<HTMLElement>('.settings h1');
		h?.focus();
	});

	async function setOne(id: number, k: 'soon' | 'eve', v: boolean) {
		if (v && needsInstall()) {
			app.installOpen = true;
			return;
		}
		if (v) {
			try {
				await ensureSubscribed();
			} catch (e) {
				app.showToast(e instanceof Error ? e.message : '알림을 켜지 못했어요.');
				return;
			}
		}
		const fav = app.favs.find((f) => f.id === id);
		if (!fav) return;
		const r = await app.setAlerts(fav, { [k]: v });
		if (r) alerts = r;
	}

	async function test() {
		if (needsInstall()) {
			app.installOpen = true;
			return;
		}
		testing = true;
		try {
			const endpoint = await ensureSubscribed();
			const r = await api.pushTest(endpoint);
			app.showToast(r.sent > 0 ? '테스트 알림을 보냈어요.' : '보내지 못했어요. 구독을 다시 확인해 주세요.');
		} catch (e) {
			app.showToast(e instanceof Error ? e.message : '보내지 못했어요.');
		} finally {
			testing = false;
		}
	}

	async function rename(id: number, cur: string) {
		const name = prompt('장소 이름 (예: 집, 회사)', cur)?.trim();
		if (!name || name === cur) return;
		const fav = app.favs.find((f) => f.id === id);
		if (fav) await app.rename(fav, name.slice(0, 20));
		if (alerts) alerts = { ...alerts, favorites: alerts.favorites.map((f) => (f.id === id ? { ...f, name } : f)) };
	}

	async function logout() {
		// 푸시 구독은 남겨 둔다: 서버 알림은 로그아웃과 상관없이 계속된다(§2)
		await app.logout();
		onclose();
	}

	// 정보 칸을 짧은 시간 안에 3번 누르면 로그인(관리자 숨은 입구)
	let infoTaps = 0;
	let infoTimer: ReturnType<typeof setTimeout> | null = null;
	function infoTap() {
		if (app.admin) return;
		infoTaps++;
		if (infoTimer) clearTimeout(infoTimer);
		infoTimer = setTimeout(() => (infoTaps = 0), 700);
		if (infoTaps >= 3) {
			infoTaps = 0;
			app.loginOpen = true;
		}
	}

	function openMotion() {
		app.motionOpen = true;
		history.pushState({ settings: true, motion: true }, '');
	}

	const md = (iso?: string | null) => {
		if (!iso) return '';
		const p = kst(iso);
		return `${p.mo}월 ${p.d}일`;
	};

	const lastLine = $derived.by(() => {
		const lc = info?.last_collected;
		const parts: string[] = [];
		const at = (x: any) => x?.at ?? x?.base ?? null;
		if (lc) {
			if (at(lc.getUltraSrtNcst)) parts.push(`${hm(at(lc.getUltraSrtNcst))} 실황`);
			if (at(lc.getVilageFcst)) parts.push(`${hm(at(lc.getVilageFcst))} 단기예보`);
		} else if (app.timeline) {
			const s = app.timeline.sources;
			const v = Object.values(s).find((x) => x.kind === 'vilage' && x.base);
			const u = Object.values(s).find((x) => x.kind === 'ultra' && x.base);
			if (u?.base) parts.push(`${hm(u.base)} 초단기예보`);
			if (v?.base) parts.push(`${hm(v.base)} 단기예보 발표분`);
		}
		return parts.join(' · ');
	});
	const passkeys = $derived<any[]>(info?.admin?.passkeys ?? []);
</script>

<div class="settings" role="dialog" aria-modal="true" aria-label="설정">
	<header>
		<button type="button" class="icon-btn" aria-label="뒤로" onclick={onclose}><Icon name="back" size={22} /></button>
		<h1 tabindex="-1">설정</h1>
	</header>
	<div class="content">
		{#if app.admin}
			<section>
				<h2>비 알림</h2>
				<div class="card">
					<div class="grid head"><span>장소</span><span>곧 비</span><span>전날 밤</span></div>
					{#if alerts}
						{#each alerts.favorites as f (f.id)}
							<div class="grid">
								<button type="button" class="pname" onclick={() => rename(f.id, f.name)} aria-label="{f.name} 이름 바꾸기"
									>{f.name}</button
								>
								<label class="sw"
									><input
										type="checkbox"
										role="switch"
										checked={f.soon}
										aria-label="{f.name} 곧 비 알림"
										onchange={(e) => setOne(f.id, 'soon', e.currentTarget.checked)}
									/></label
								>
								<label class="sw"
									><input
										type="checkbox"
										role="switch"
										checked={f.eve}
										aria-label="{f.name} 전날 밤 예고"
										onchange={(e) => setOne(f.id, 'eve', e.currentTarget.checked)}
									/></label
								>
							</div>
						{:else}
							<div class="pad muted">즐겨찾기가 없어요. 장소 시트에서 별을 눌러 추가하세요.</div>
						{/each}
					{:else}
						<div class="pad muted">{alertsErr || '불러오는 중…'}</div>
					{/if}
				</div>
				<p class="hint">
					곧 비: 1~3시간 안에 비·눈이 시작되면 · 전날 밤: 매일 22시, 내일 비 예보가 있으면 · 장소 이름을 누르면 바꿀 수 있어요
				</p>
				<button type="button" class="pill" onclick={test} disabled={testing}>{testing ? '보내는 중…' : '테스트 알림 보내기'}</button>
			</section>
			<section>
				<h2>관리자</h2>
				<div class="card">
					<div class="item">
						<span class="grow">패스키</span>
						<span class="muted"
							>{#if passkeys.length}{#each passkeys as k, i (k.id)}{i ? ', ' : ''}{k.current ? '이 ' : ''}{k.label} · {md(k.created_at)} 등록{/each}{:else}–{/if}</span
						>
					</div>
					<div class="item">
						<span class="grow">수집 중인 장소</span>
						<span class="muted"
							>{info?.admin
								? `${info.admin.collect_grids ?? '–'}곳 · 오늘 호출 ${info.admin.calls_today?.total ?? '–'}회`
								: '–'}</span
						>
					</div>
					<button type="button" class="item danger" onclick={logout}>로그아웃</button>
				</div>
			</section>
		{/if}
		<section>
			<h2>보기</h2>
			<div class="card">
				<button type="button" class="item" onclick={openMotion}>
					<span class="grow">날씨 모션 보기</span><span class="chev" aria-hidden="true">›</span>
				</button>
			</div>
		</section>
		<section>
			<h2>정보</h2>
			<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
			<div class="card pad info" onclick={infoTap}>
				<span>데이터: {info?.data_source ?? '기상청 단기예보 조회서비스 (공공데이터포털)'}</span>
				{#if lastLine}<span>마지막 수집: {lastLine}</span>{/if}
			</div>
		</section>
	</div>
</div>

<style>
	.settings {
		position: fixed;
		inset: 0;
		z-index: 40;
		background: var(--page-2);
		color: var(--text);
		overflow-y: auto;
		animation: in 0.2s;
	}
	header {
		position: sticky;
		top: 0;
		display: flex;
		align-items: center;
		gap: 4px;
		padding: calc(6px + env(safe-area-inset-top)) 8px 6px;
		background: var(--surface);
		border-bottom: 1px solid var(--line-2);
		z-index: 1;
	}
	h1 {
		margin: 0;
		font-size: 17px;
		outline: none;
	}
	.content {
		max-width: 560px;
		margin: 0 auto;
		padding: 16px 16px calc(24px + env(safe-area-inset-bottom));
		display: flex;
		flex-direction: column;
		gap: 20px;
	}
	section {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	h2 {
		margin: 0 4px;
		font-size: 13px;
		font-weight: 500;
		color: var(--text-3);
	}
	.card {
		background: var(--surface);
		border-radius: 12px;
		overflow: hidden;
	}
	.pad {
		padding: 12px 14px;
	}
	.info {
		font-size: 13px;
		color: var(--text-2);
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 72px 72px;
		align-items: center;
		min-height: 48px;
		padding: 0 14px;
		border-bottom: 1px solid var(--line);
	}
	.grid:last-child {
		border-bottom: 0;
	}
	.grid.head {
		min-height: 0;
		padding: 8px 14px;
		font-size: 12px;
		color: var(--text-3);
	}
	.grid > span {
		text-align: center;
	}
	.sw {
		display: flex;
		align-items: center;
		justify-content: center;
		min-width: 44px;
		min-height: 44px;
		cursor: pointer;
	}
	.grid.head > span:first-child {
		text-align: left;
	}
	.pname {
		text-align: left;
		font: inherit;
		font-size: 15px;
		color: var(--text);
		background: none;
		border: 0;
		min-height: 44px;
		padding: 0;
	}
	input[type='checkbox'] {
		width: 22px;
		height: 22px;
		accent-color: var(--accent);
		margin: 0;
	}
	.hint {
		margin: 0 4px;
		font-size: 12px;
		color: var(--text-3);
	}
	.pill {
		align-self: flex-start;
		min-height: 44px;
		padding: 0 16px;
		border: 1px solid var(--border-strong);
		border-radius: 22px;
		background: var(--surface);
		font: inherit;
		font-size: 13px;
		color: var(--text);
	}
	.item {
		display: flex;
		align-items: center;
		gap: 8px;
		width: 100%;
		min-height: 52px;
		padding: 0 14px;
		border: 0;
		border-bottom: 1px solid var(--line);
		background: none;
		font: inherit;
		font-size: 15px;
		color: var(--text);
		text-align: left;
		box-sizing: border-box;
	}
	.item:last-child {
		border-bottom: 0;
	}
	.chev {
		font-size: 20px;
		color: var(--text-4);
	}
	.grow {
		flex-grow: 1;
	}
	.muted {
		font-size: 13px;
		color: var(--text-3);
		text-align: right;
	}
	.danger {
		color: var(--danger-text);
	}
	.icon-btn {
		width: 44px;
		height: 44px;
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: 0;
		color: var(--text);
	}
	@keyframes in {
		from {
			opacity: 0;
			transform: translateX(24px);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.settings {
			animation: none;
		}
	}
</style>
