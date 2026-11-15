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

// 기온 → 색. 차트 선·일별 막대가 같은 눈금을 쓴다(추우면 파랑, 선선하면 청록, 따뜻하면 노랑·주황, 더우면 빨강)
const TEMP_STOPS: [number, [number, number, number]][] = [
	[-15, [110, 120, 230]],
	[-3, [84, 150, 235]],
	[6, [74, 182, 205]],
	[14, [236, 184, 74]],
	[22, [240, 136, 72]],
	[31, [226, 80, 80]]
];
export function tempColor(t: number): string {
	const s = TEMP_STOPS;
	if (t <= s[0][0]) return `rgb(${s[0][1].join(',')})`;
	for (let i = 1; i < s.length; i++) {
		if (t <= s[i][0]) {
			const [t0, a] = s[i - 1],
				[t1, b] = s[i];
			const k = (t - t0) / (t1 - t0);
			return `rgb(${a.map((v, j) => Math.round(v + (b[j] - v) * k)).join(',')})`;
		}
	}
	return `rgb(${s[s.length - 1][1].join(',')})`;
}
