// 날씨 모션 보기(/motion): 날씨·시간대별 배경 장면을 가짜 타임라인으로 만든다.
import { sceneOf, type Scene } from './scene';
import type { Pty, Sky, Timeline } from './types';

type Light = 'day' | 'night' | 'dawn' | 'dusk';

const DATE = '2026-11-11';
const SUN = { rise: `${DATE}T07:08:00+09:00`, set: `${DATE}T17:24:00+09:00` };
const AT: Record<Light, string> = { day: '12:00', night: '23:00', dawn: '07:10', dusk: '17:20' };

function demo(light: Light, sky: Sky, pty: Pty, mmh = 0, u = 0, moon = 0.25): Scene {
	const now = `${DATE}T${AT[light]}:00+09:00`;
	const wet = pty !== 'none';
	const slot = {
		t: now,
		h: 1,
		phase: 'now',
		res: '1h',
		tmp: 5,
		feels: 5,
		sky,
		pty,
		wet,
		pcp: wet && mmh ? { mm: mmh, text: `${mmh}mm` } : null,
		pop: 0,
		reh: 50,
		wsd: Math.abs(u),
		vec: 0,
		dir16: null,
		uuu: u,
		vvv: 0,
		sno: null,
		src: {},
		estimated: false,
		partial: false
	};
	const tl = { now, moon_phase: moon, slots: [slot], daily: [{ date: DATE, label: '오늘', sun: SUN, moon_phase: moon }] };
	return sceneOf(tl as unknown as Timeline);
}

export const MOTION_DEMOS: { name: string; scene: Scene }[] = [
	{ name: '맑음 · 낮', scene: demo('day', 'clear', 'none') },
	{ name: '맑음 · 새벽', scene: demo('dawn', 'clear', 'none') },
	{ name: '맑음 · 저녁', scene: demo('dusk', 'clear', 'none') },
	{ name: '맑음 · 밤 (상현)', scene: demo('night', 'clear', 'none', 0, 0, 0.25) },
	{ name: '맑음 · 밤 (보름)', scene: demo('night', 'clear', 'none', 0, 0, 0.5) },
	{ name: '맑음 · 밤 (그믐)', scene: demo('night', 'clear', 'none', 0, 0, 0.95) },
	{ name: '구름 많음 · 낮', scene: demo('day', 'partly', 'none') },
	{ name: '구름 많음 · 밤', scene: demo('night', 'partly', 'none', 0, 0, 0.6) },
	{ name: '흐림 · 낮', scene: demo('day', 'cloudy', 'none') },
	{ name: '흐림 · 밤', scene: demo('night', 'cloudy', 'none') },
	{ name: '이슬비 0.3mm/h', scene: demo('day', 'cloudy', 'drizzle', 0.3) },
	{ name: '비 1mm/h', scene: demo('day', 'cloudy', 'rain', 1) },
	{ name: '비 5mm/h · 동풍 5m/s', scene: demo('day', 'cloudy', 'rain', 5, 5) },
	{ name: '폭우 30mm/h · 서풍 10m/s', scene: demo('night', 'cloudy', 'rain', 30, -10) },
	{ name: '눈 0.5mm/h', scene: demo('day', 'cloudy', 'snow', 0.5) },
	{ name: '눈 3mm/h · 동풍 6m/s', scene: demo('night', 'cloudy', 'snow', 3, 6) },
	{ name: '비/눈 2mm/h', scene: demo('day', 'cloudy', 'rain_snow', 2) },
	{ name: '소나기 10mm/h', scene: demo('day', 'cloudy', 'shower', 10) }
];
