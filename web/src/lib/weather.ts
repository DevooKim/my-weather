// 아이콘·낮밤·배경 판단
import type { IconName } from './components/Icon.svelte';
import { hasLightning, isSnowy, kst } from './format';
import type { Day, Pty, Sky, Slot } from './types';

export function sunFor(daily: Day[] | null | undefined, iso: string) {
	const date = kst(iso).date;
	return daily?.find((d) => d.date === date)?.sun ?? null;
}

/** 낮인지. 일출·일몰이 없으면 06~18시를 낮으로 본다. */
export function isDay(iso: string, daily: Day[] | null | undefined, midOffsetH = 0) {
	const ms = new Date(iso).getTime() + midOffsetH * 3600_000;
	const sun = sunFor(daily, iso);
	if (sun) return ms >= Date.parse(sun.rise) && ms < Date.parse(sun.set);
	const h = kst(ms).h;
	return h >= 6 && h < 18;
}

export function iconFor(
	s: { sky: Sky | null; pty: Pty | null; wet: boolean; lgt?: number | null },
	day: boolean
): IconName {
	if (hasLightning(s)) return 'thunder';
	if (s.wet && s.pty && s.pty !== 'none') {
		if (s.pty === 'rain_snow' || s.pty === 'drizzle_snow') return 'rain_snow';
		if (isSnowy(s.pty)) return 'snow';
		return 'rain';
	}
	switch (s.sky) {
		case 'clear':
			return day ? 'sun' : 'moon';
		case 'partly':
			return day ? 'partly-day' : 'partly-night';
		case 'cloudy':
			return 'cloud';
	}
	return 'unknown';
}

/** 아이콘 색 (라이트/다크 모두 CSS 변수) */
export function iconColor(n: IconName) {
	switch (n) {
		case 'sun':
		case 'partly-day':
			return 'var(--icon-sun)';
		case 'rain':
		case 'snow':
		case 'rain_snow':
			return 'var(--icon-rain)';
		case 'thunder':
			return 'var(--icon-sun)';
		default:
			return 'var(--icon-gray)';
	}
}

export function slotIcon(s: Slot, daily: Day[] | null | undefined) {
	return iconFor(s, isDay(s.t, daily, s.h / 2));
}

/** 시간당 강수량 (mm/h). 3시간 칸이면 나눈다. 연장 코드는 대표값. */
export function precipRate(s: Slot) {
	if (!s.wet || !s.pty || s.pty === 'none') return 0;
	const a = s.pcp;
	let mm = a ? a.mm : 0;
	if (!mm) mm = s.pty === 'drizzle' || s.pty === 'flurry' ? 0.3 : 1; // 강수형태는 있는데 양이 없을 때
	return mm / (s.h || 1);
}
