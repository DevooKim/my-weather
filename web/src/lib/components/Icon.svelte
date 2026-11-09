<script lang="ts" module>
	export type IconName =
		| 'sun'
		| 'moon'
		| 'partly-day'
		| 'partly-night'
		| 'cloud'
		| 'rain'
		| 'snow'
		| 'rain_snow'
		| 'unknown'
		| 'chevron-down'
		| 'chevron-up'
		| 'menu'
		| 'search'
		| 'locate'
		| 'star'
		| 'star-fill'
		| 'bell'
		| 'bell-fill'
		| 'grip'
		| 'back'
		| 'close'
		| 'share'
		| 'faceid'
		| 'arrow';
</script>

<script lang="ts">
	let {
		name,
		size = 20,
		color = 'currentColor',
		style = ''
	}: { name: IconName; size?: number; color?: string; style?: string } = $props();
</script>

<svg
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke={color}
	stroke-width="1.8"
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden="true"
	{style}
>
	{#if name === 'sun'}
		<circle cx="12" cy="12" r="4" />
		<path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
	{:else if name === 'moon'}
		<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z" />
	{:else if name === 'partly-day'}
		<path d="M8 3.5v1.2M3.5 8h1.2M4.8 4.8l.9.9M11.2 4.8l-.9.9" />
		<path d="M10.6 9.2A3.2 3.2 0 1 0 5.3 10.6" />
		<path d="M8.5 20h9a3.6 3.6 0 0 0 .4-7.2 5.2 5.2 0 0 0-10 1.3A3 3 0 0 0 8.5 20z" />
	{:else if name === 'partly-night'}
		<path d="M10.5 8.6A4.6 4.6 0 0 1 6 3.2a4.8 4.8 0 1 0 4.5 5.4z" />
		<path d="M8.5 20h9a3.6 3.6 0 0 0 .4-7.2 5.2 5.2 0 0 0-10 1.3A3 3 0 0 0 8.5 20z" />
	{:else if name === 'cloud'}
		<path d="M7 18h10a4 4 0 0 0 .5-8 6 6 0 0 0-11.5 1.5A3.3 3.3 0 0 0 7 18z" />
	{:else if name === 'rain'}
		<path d="M7 14h10a3.5 3.5 0 0 0 .4-7 5.5 5.5 0 0 0-10.6 1.3A2.9 2.9 0 0 0 7 14z" />
		<path d="M8 17l-1 3M12 17l-1 3M16 17l-1 3" />
	{:else if name === 'snow'}
		<path d="M7 14h10a3.5 3.5 0 0 0 .4-7 5.5 5.5 0 0 0-10.6 1.3A2.9 2.9 0 0 0 7 14z" />
		<path d="M8 18h.01M12 18h.01M16 18h.01M10 21h.01M14 21h.01" stroke-width="2.6" />
	{:else if name === 'rain_snow'}
		<path d="M7 14h10a3.5 3.5 0 0 0 .4-7 5.5 5.5 0 0 0-10.6 1.3A2.9 2.9 0 0 0 7 14z" />
		<path d="M8 17l-1 3M16 17l-1 3" />
		<path d="M12 18h.01" stroke-width="2.6" />
	{:else if name === 'unknown'}
		<path d="M9 12h6" />
	{:else if name === 'chevron-down'}
		<path d="M6 9l6 6 6-6" stroke-width="2" />
	{:else if name === 'chevron-up'}
		<path d="M18 15l-6-6-6 6" stroke-width="2" />
	{:else if name === 'menu'}
		<path d="M4 7h16M4 12h16M4 17h16" stroke-width="2" />
	{:else if name === 'search'}
		<circle cx="11" cy="11" r="7" stroke-width="2" /><path d="M20 20l-4-4" stroke-width="2" />
	{:else if name === 'locate'}
		<circle cx="12" cy="12" r="3" stroke-width="2" /><circle cx="12" cy="12" r="8" stroke-width="2" />
	{:else if name === 'star'}
		<path d="M12 3l2.8 5.7 6.2.9-4.5 4.4 1 6.2-5.5-2.9-5.5 2.9 1-6.2L3 9.6l6.2-.9z" />
	{:else if name === 'star-fill'}
		<path d="M12 3l2.8 5.7 6.2.9-4.5 4.4 1 6.2-5.5-2.9-5.5 2.9 1-6.2L3 9.6l6.2-.9z" fill={color} />
	{:else if name === 'bell'}
		<path d="M12 3a6 6 0 0 0-6 6v4l-2 3h16l-2-3V9a6 6 0 0 0-6-6zm-2 15a2 2 0 0 0 4 0z" />
	{:else if name === 'bell-fill'}
		<path d="M12 3a6 6 0 0 0-6 6v4l-2 3h16l-2-3V9a6 6 0 0 0-6-6zm-2 15a2 2 0 0 0 4 0z" fill={color} stroke="none" />
	{:else if name === 'grip'}
		<path d="M5 9h14M5 15h14" stroke-width="2" />
	{:else if name === 'back'}
		<path d="M15 6l-6 6 6 6" stroke-width="2" />
	{:else if name === 'close'}
		<path d="M6 6l12 12M18 6L6 18" stroke-width="2" />
	{:else if name === 'share'}
		<path d="M12 15V3M8 7l4-4 4 4M6 11H5v10h14V11h-1" stroke-width="2" />
	{:else if name === 'faceid'}
		<path d="M4 8V6a2 2 0 0 1 2-2h2M16 4h2a2 2 0 0 1 2 2v2M20 16v2a2 2 0 0 1-2 2h-2M8 20H6a2 2 0 0 1-2-2v-2M9 9v1M15 9v1M12 9v4h-1M9 16c1.5 1 4.5 1 6 0" />
	{:else if name === 'arrow'}
		<path d="M12 20V4M6 10l6-6 6 6" stroke-width="2.2" />
	{/if}
</svg>
