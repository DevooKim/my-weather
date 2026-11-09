// 배경 장면: 지금 날씨 + 시간(일출·일몰) → 색, 해/달, 비·눈 세기와 기울기
import { isSnowy } from './format';
import type { Timeline } from './types';
import { precipRate, sunFor } from './weather';

export type Light = 'day' | 'night' | 'dawn' | 'dusk';

export interface Scene {
	light: Light;
	kind: 'clear' | 'partly' | 'cloudy' | 'rain' | 'snow' | 'mixed';
	/** mm/h */
	rate: number;
	/** 동서(+동쪽으로 부는) 바람 m/s, 남북(+북쪽으로) */
	u: number;
	v: number;
	moonPhase: number;
	/** 배경 그라데이션 위·아래 */
	top: string;
	bottom: string;
}

const TWILIGHT_MS = 45 * 60_000;

export function sceneOf(tl: Timeline | null): Scene {
	const base: Scene = {
		light: 'night',
		kind: 'cloudy',
		rate: 0,
		u: 0,
		v: 0,
		moonPhase: 0.5,
		top: '#2B3440',
		bottom: '#36414F'
	};
	if (!tl) return base;
	const s = tl.slots.find((x) => x.phase === 'now') ?? tl.slots.find((x) => x.phase === 'future');
	if (!s) return base;
	const now = new Date(tl.now).getTime();
	const sun = sunFor(tl.daily, tl.now);
	let light: Light = 'day';
	if (sun) {
		const rise = Date.parse(sun.rise),
			set = Date.parse(sun.set);
		if (Math.abs(now - rise) < TWILIGHT_MS) light = 'dawn';
		else if (Math.abs(now - set) < TWILIGHT_MS) light = 'dusk';
		else light = now > rise && now < set ? 'day' : 'night';
	}
	// 지금 칸에 하늘상태가 없으면(실황에는 없음) 다음 칸 예보 값을 빌린다
	const sky = s.sky ?? tl.slots.find((x) => x.phase === 'future' && x.sky)?.sky ?? null;
	const rate = precipRate(s);
	let kind: Scene['kind'] = sky === 'clear' ? 'clear' : sky === 'partly' ? 'partly' : 'cloudy';
	if (rate > 0) kind = s.pty === 'rain_snow' || s.pty === 'drizzle_snow' ? 'mixed' : isSnowy(s.pty) ? 'snow' : 'rain';
	const today = tl.daily?.find((d) => d.label === '오늘');
	const [top, bottom] = palette(light, kind);
	return {
		light,
		kind,
		rate,
		u: s.uuu ?? 0,
		v: s.vvv ?? 0,
		moonPhase: today?.moon_phase ?? 0.5,
		top,
		bottom
	};
}

// 흰 글자와 대비 4.5:1 이상이 되도록 어둡게 고른 색
function palette(light: Light, kind: Scene['kind']): [string, string] {
	const wet = kind === 'rain' || kind === 'snow' || kind === 'mixed';
	if (wet) return light === 'day' ? ['#2B3440', '#3A4553'] : ['#1E242C', '#2B3440'];
	if (light === 'dawn') return ['#3B3557', '#8C5A4A'];
	if (light === 'dusk') return ['#2E2F55', '#9A5540'];
	if (light === 'night') return kind === 'clear' ? ['#121A2C', '#24304A'] : ['#1D232B', '#2D353F'];
	if (kind === 'clear') return ['#2C5A8C', '#3F6FA3'];
	if (kind === 'partly') return ['#35587D', '#4A6A8B'];
	return ['#3B4552', '#4E5966'];
}
