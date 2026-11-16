<script lang="ts">
	import '../app.css';
	import { registerSW } from '$lib/push';
	import { watchTheme } from '$lib/theme';
	import { onMount } from 'svelte';
	let { children } = $props();
	onMount(() => {
		watchTheme();
		registerSW();
	});
</script>

{@render children()}
