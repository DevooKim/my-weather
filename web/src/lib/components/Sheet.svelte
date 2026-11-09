<script lang="ts">
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';

	let {
		label,
		onclose,
		closable = true,
		tall = false,
		children
	}: { label: string; onclose: () => void; closable?: boolean; tall?: boolean; children: Snippet } = $props();

	let box: HTMLElement | undefined = $state();
	let dragY = $state(0);
	let startY: number | null = null;

	onMount(() => {
		const prev = document.activeElement as HTMLElement | null;
		const first = box?.querySelector<HTMLElement>('input, button:not([data-handle])');
		// iOS는 시트가 뜰 때 input에 바로 포커스를 주면 키보드가 올라오므로 시트 자체에 둔다
		(first && first.tagName !== 'INPUT' ? first : box)?.focus({ preventScroll: true });
		const prevOverflow = document.documentElement.style.overflow;
		document.documentElement.style.overflow = 'hidden';
		return () => {
			document.documentElement.style.overflow = prevOverflow;
			prev?.focus?.({ preventScroll: true });
		};
	});

	function key(e: KeyboardEvent) {
		if (e.key === 'Escape' && closable) {
			e.stopPropagation();
			onclose();
		}
		if (e.key === 'Tab' && box) {
			const f = [...box.querySelectorAll<HTMLElement>('button, input, [tabindex="0"], a[href]')].filter(
				(x) => !x.hasAttribute('disabled') && x.offsetParent !== null
			);
			if (!f.length) return;
			const a = f[0],
				b = f[f.length - 1];
			if (e.shiftKey && document.activeElement === a) {
				b.focus();
				e.preventDefault();
			} else if (!e.shiftKey && document.activeElement === b) {
				a.focus();
				e.preventDefault();
			}
		}
	}

	// 손잡이를 아래로 끌면 닫힘
	function down(e: PointerEvent) {
		if (!closable) return;
		startY = e.clientY;
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
	}
	function move(e: PointerEvent) {
		if (startY == null) return;
		dragY = Math.max(0, e.clientY - startY);
	}
	function up() {
		if (startY == null) return;
		startY = null;
		if (dragY > 90) onclose();
		dragY = 0;
	}
</script>

<div class="wrap" role="presentation" onkeydown={key}>
	<div class="backdrop" role="presentation" onclick={() => closable && onclose()}></div>
	<div
		bind:this={box}
		class="sheet"
		class:tall
		role="dialog"
		aria-modal="true"
		aria-label={label}
		tabindex="-1"
		style={dragY ? `transform: translateY(${dragY}px); transition: none` : ''}
	>
		<button
			type="button"
			class="handle"
			data-handle
			aria-label={closable ? '닫기' : label}
			tabindex={closable ? 0 : -1}
			onclick={() => closable && onclose()}
			onpointerdown={down}
			onpointermove={move}
			onpointerup={up}
			onpointercancel={up}
		>
			<span></span>
		</button>
		{@render children()}
	</div>
</div>

<style>
	.wrap {
		position: fixed;
		inset: 0;
		z-index: 50;
	}
	.backdrop {
		position: absolute;
		inset: 0;
		background: rgba(0, 0, 0, 0.45);
		animation: fade 0.2s;
	}
	.sheet {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 0;
		max-height: min(86vh, 640px);
		background: var(--surface);
		color: var(--text);
		border-radius: 16px 16px 0 0;
		padding: 0 16px calc(16px + env(safe-area-inset-bottom));
		box-sizing: border-box;
		display: flex;
		flex-direction: column;
		gap: 8px;
		box-shadow: 0 -4px 16px rgba(0, 0, 0, 0.16);
		overflow-y: auto;
		overscroll-behavior: contain;
		animation: up 0.24s cubic-bezier(0.2, 0.8, 0.2, 1);
		transition: transform 0.2s;
		outline: none;
	}
	.sheet.tall {
		height: min(86vh, 640px);
	}
	.handle {
		align-self: center;
		width: 64px;
		height: 44px;
		margin-bottom: -12px;
		flex: none;
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: 0;
		padding: 0;
		touch-action: none;
		cursor: grab;
	}
	.handle span {
		width: 40px;
		height: 4px;
		border-radius: 2px;
		background: var(--border-strong);
	}
	@media (min-width: 720px) {
		.sheet {
			left: 50%;
			right: auto;
			bottom: auto;
			top: 50%;
			width: 440px;
			transform: translate(-50%, -50%);
			border-radius: 16px;
			animation: fade 0.2s;
		}
		.handle {
			display: none;
		}
		.sheet {
			padding-top: 16px;
		}
	}
	@keyframes up {
		from {
			transform: translateY(100%);
		}
	}
	@keyframes fade {
		from {
			opacity: 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.sheet,
		.backdrop {
			animation: none;
		}
	}
</style>
