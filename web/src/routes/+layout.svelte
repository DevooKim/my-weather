<script lang="ts">
	import '../app.css';
	import { registerSW } from '$lib/push';
	import { onMount } from 'svelte';
	let { children } = $props();
	onMount(() => {
		registerSW();
	});
</script>

{@render children()}
