<script lang="ts">
	import { ApiError } from '$lib/api';
	import { app } from '$lib/app.svelte';
	import { loginWithPasskey, passkeySupported, registerPasskey } from '$lib/webauthn';
	import Icon from './Icon.svelte';
	import Sheet from './Sheet.svelte';

	let token = $state('');
	let busy = $state<'login' | 'register' | null>(null);
	let err = $state('');

	function msg(e: unknown) {
		if (e instanceof ApiError) {
			if (e.status === 403) return '등록 코드가 맞지 않거나 만료됐어요. 서버에서 새 코드를 받아 주세요.';
			if (e.status === 401) return '이 패스키로 로그인할 수 없어요. 등록한 기기인지 확인해 주세요.';
			if (e.status === 409) return '이 기기는 이미 등록돼 있어요. 패스키로 로그인하세요.';
			if (e.status === 400) return `${e.message} 다시 시도해 주세요.`;
			if (e.status === 404) return '서버에 아직 로그인 기능이 없어요.';
			if (e.status === 503) return '서버에 패스키 설정이 아직 없어요.';
			return e.message;
		}
		return e instanceof Error ? e.message : '실패했어요.';
	}

	async function login() {
		err = '';
		busy = 'login';
		try {
			await loginWithPasskey();
			await app.afterLogin();
		} catch (e) {
			err = msg(e);
		} finally {
			busy = null;
		}
	}
	async function register(e: SubmitEvent) {
		e.preventDefault();
		if (!token.trim()) {
			err = '등록 코드를 넣어 주세요.';
			return;
		}
		err = '';
		busy = 'register';
		try {
			await registerPasskey(token);
			token = '';
			await app.afterLogin();
		} catch (e) {
			err = msg(e);
		} finally {
			busy = null;
		}
	}
</script>

<Sheet label="로그인" onclose={() => (app.loginOpen = false)}>
	<div class="body">
		<h1>로그인</h1>
		<p class="lead">로그인하면 이 계정의 즐겨찾기가 서버에 저장되고, 실황 수집과 비 알림을 쓸 수 있습니다. iPhone은 Face ID, 안드로이드 Chrome은 지문·PIN으로 등록합니다.</p>
		{#if !passkeySupported()}
			<p class="err" role="alert">이 브라우저는 패스키를 지원하지 않아요.</p>
		{/if}
		<button type="button" class="primary" onclick={login} disabled={!!busy || !passkeySupported()}>
			<Icon name="faceid" size={22} />
			{busy === 'login' ? '확인 중…' : '패스키로 로그인'}
		</button>
		<div class="or"><span></span>처음이거나 폰을 바꿨다면<span></span></div>
		<form onsubmit={register}>
			<label>
				등록 코드
				<input
					type="text"
					bind:value={token}
					placeholder="MW-XXXX-XXXX-XXXX"
					autocomplete="one-time-code"
					autocapitalize="characters"
					spellcheck="false"
				/>
			</label>
			<button type="submit" class="secondary" disabled={!!busy || !passkeySupported()}>
				{busy === 'register' ? '등록 중…' : '이 기기를 패스키로 등록'}
			</button>
		</form>
		{#if err}<p class="err" role="alert">{err}</p>{/if}
	</div>
</Sheet>

<style>
	.body {
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 0 4px 4px;
	}
	h1 {
		margin: 4px 0 0;
		font-size: 20px;
	}
	.lead {
		margin: 0;
		font-size: 14px;
		color: var(--text-2);
	}
	.primary {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		min-height: 52px;
		border: 0;
		border-radius: 12px;
		background: var(--btn-dark);
		color: var(--btn-dark-text);
		font: inherit;
		font-size: 16px;
		font-weight: 700;
	}
	.or {
		display: flex;
		align-items: center;
		gap: 8px;
		color: var(--text-3);
		font-size: 12px;
	}
	.or span {
		flex-grow: 1;
		height: 1px;
		background: var(--line);
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 6px;
		font-size: 13px;
		color: var(--text-2);
	}
	input {
		height: 44px;
		padding: 0 12px;
		border: 1px solid var(--border-strong);
		border-radius: 10px;
		font: inherit;
		font-size: 16px;
		color: var(--text);
		background: var(--surface);
		letter-spacing: 0.5px;
	}
	.secondary {
		min-height: 48px;
		border: 1px solid var(--text);
		border-radius: 12px;
		background: var(--surface);
		color: var(--text);
		font: inherit;
		font-size: 15px;
		font-weight: 500;
	}
	button:disabled {
		opacity: 0.6;
	}
	.err {
		margin: 0;
		font-size: 14px;
		color: var(--danger-text);
	}
</style>
