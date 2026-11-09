<script lang="ts">
	import { app } from '$lib/app.svelte';
	import Icon from './Icon.svelte';
	import Sheet from './Sheet.svelte';
</script>

<Sheet label="알림을 받으려면 홈 화면에 추가" onclose={() => (app.installOpen = false)}>
	<div class="body">
		<h1>알림을 받으려면 홈 화면에 추가하세요</h1>
		<p class="lead">iPhone은 홈 화면에 추가한 웹앱에서만 알림을 보낼 수 있어요.</p>
		<ol>
			<li>
				<span class="n">1</span><span
					>Safari 아래쪽 <b>공유</b> 버튼 <Icon name="share" size={18} color="var(--accent)" style="vertical-align:-3px" /> 누르기</span
				>
			</li>
			<li><span class="n">2</span><span><b>홈 화면에 추가</b> 고르기</span></li>
			<li><span class="n">3</span><span>홈 화면의 앱을 열고 <b>종 버튼</b>을 다시 누르기</span></li>
		</ol>
		<p class="hint">홈 화면 앱에서는 로그인이 따로 유지되니 한 번 더 로그인해야 할 수 있어요.</p>
		<button type="button" class="primary" onclick={() => (app.installOpen = false)}>알겠어요</button>
	</div>
</Sheet>

<style>
	.body {
		display: flex;
		flex-direction: column;
		gap: 14px;
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
	ol {
		margin: 0;
		padding: 0;
		list-style: none;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	li {
		display: flex;
		align-items: center;
		gap: 12px;
		font-size: 15px;
	}
	.n {
		flex: none;
		width: 28px;
		height: 28px;
		border-radius: 14px;
		background: var(--accent-weak);
		color: var(--accent-text);
		display: flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
		font-size: 14px;
	}
	.hint {
		margin: 0;
		font-size: 12px;
		color: var(--text-3);
	}
	.primary {
		margin-top: 8px;
		min-height: 48px;
		border: 0;
		border-radius: 12px;
		background: var(--btn-dark);
		color: var(--btn-dark-text);
		font: inherit;
		font-size: 15px;
		font-weight: 500;
	}
</style>
