// 표시용 문자열. 시각은 기기 시간대와 상관없이 KST로 다룬다.
import type { Amount, Day, Pty, Sky, Slot } from './types';

const KST = 9 * 3600 * 1000;
const WEEK = ['일', '월', '화', '수', '목', '금', '토'];

export interface KstParts {
	y: number;
	mo: number; // 1~12
	d: number;
	h: number;
	mi: number;
	wd: number;
	date: string; // YYYY-MM-DD
	ms: number;
}

export function kst(iso: string | number | Date): KstParts {
	const ms = typeof iso === 'number' ? iso : new Date(iso).getTime();
	const k = new Date(ms + KST);
	const y = k.getUTCFullYear(),
		mo = k.getUTCMonth() + 1,
		d = k.getUTCDate();
	return {
		y,
		mo,
		d,
		h: k.getUTCHours(),
		mi: k.getUTCMinutes(),
		wd: k.getUTCDay(),
		date: `${y}-${String(mo).padStart(2, '0')}-${String(d).padStart(2, '0')}`,
		ms
	};
}

function dayDiff(a: string, b: string) {
	return Math.round((Date.parse(a + 'T00:00:00Z') - Date.parse(b + 'T00:00:00Z')) / 86400000);
}

const REL: Record<number, string> = { [-1]: '어제', 0: '오늘', 1: '내일', 2: '모레' };

/** "어제 9일", "오늘 10일", "12일 목" */
export function dayLabel(iso: string, nowIso: string) {
	const p = kst(iso);
	const diff = dayDiff(p.date, kst(nowIso).date);
	const rel = REL[diff];
	return rel ? `${rel} ${p.d}일` : `${p.d}일 ${WEEK[p.wd]}`;
}

export function hm(iso: string) {
	const p = kst(iso);
	return `${String(p.h).padStart(2, '0')}:${String(p.mi).padStart(2, '0')}`;
}

export function hourLabel(iso: string) {
	return `${kst(iso).h}시`;
}

export function deg(v: number | null | undefined) {
	return v == null ? '–' : `${Math.round(v)}°`;
}

export const SKY_TEXT: Record<Sky, string> = { clear: '맑음', partly: '구름 많음', cloudy: '흐림' };
export const PTY_TEXT: Record<Pty, string> = {
	none: '',
	rain: '비',
	rain_snow: '비/눈',
	snow: '눈',
	shower: '소나기',
	drizzle: '빗방울',
	drizzle_snow: '빗방울·눈날림',
	flurry: '눈날림'
};

/** 날씨 한 단어. 비가 아니라고 본 초단기 5·6·7(wet=false)은 하늘상태로 말한다. */
export function weatherWord(s: { sky: Sky | null; pty: Pty | null; wet: boolean; lgt?: number | null }) {
	const base = s.pty && s.pty !== 'none' && s.wet ? PTY_TEXT[s.pty] : s.sky ? SKY_TEXT[s.sky] : '';
	if (hasLightning(s)) return base ? `${base} · 천둥번개` : '천둥번개';
	return base;
}

/** 초단기예보 낙뢰 값이 0보다 크면 천둥번개 */
export function hasLightning(s: { lgt?: number | null }) {
	return s.lgt != null && s.lgt > 0;
}

export function isSnowy(p: Pty | null) {
	return p === 'snow' || p === 'flurry' || p === 'rain_snow' || p === 'drizzle_snow';
}

export function amountText(a: Amount | null | undefined, unit = 'mm') {
	if (!a) return '';
	if (a.code != null) return a.text; // 연장 구간: '약한 비' 등
	if (a.mm === 0 && a.max === 0) return '';
	if (a.exact) return `${round1(a.mm)}${unit}`;
	return a.text;
}

export function round1(v: number) {
	return Math.round(v * 10) / 10;
}

const DIR_KO: Record<string, string> = {
	N: '북',
	NNE: '북북동',
	NE: '북동',
	ENE: '동북동',
	E: '동',
	ESE: '동남동',
	SE: '남동',
	SSE: '남남동',
	S: '남',
	SSW: '남남서',
	SW: '남서',
	WSW: '서남서',
	W: '서',
	WNW: '서북서',
	NW: '북서',
	NNW: '북북서'
};

const WSD_CODE: Record<number, string> = { 1: '약한 바람', 2: '약간 강한 바람', 3: '강한 바람' };

export function windText(s: Slot) {
	const dir = s.dir16 ? DIR_KO[s.dir16] ?? s.dir16 : '';
	if (s.wsd != null) {
		if (s.wsd < 0.5) return '바람 거의 없음';
		return `${dir}풍 ${Math.round(s.wsd)}m/s`;
	}
	if (s.wsd_code != null) return `${dir}풍 ${WSD_CODE[s.wsd_code] ?? ''}`.trim();
	return '';
}

/** "시간당 2mm" 처럼 지금 강수 */
export function nowPrecipText(s: Slot) {
	if (!s.wet || !s.pty || s.pty === 'none') return '';
	const a = amountText(s.pcp);
	return a ? `${PTY_TEXT[s.pty]} ${a}` : PTY_TEXT[s.pty];
}

export function dailyWord(d: Day) {
	const w = d.wet && d.pty !== 'none' ? PTY_TEXT[d.pty] : '';
	const s = d.sky ? SKY_TEXT[d.sky] : '';
	if (w && s && s !== '흐림') return `${s}, ${w}`;
	return w || s || '';
}

/** 서버 요약 문장에서 큰 숫자와 겹치는 "지금 N℃, " 앞부분을 뺀다 */
export function summaryLines(text: string[] | null | undefined) {
	if (!text) return [];
	return text
		.map((t, i) => (i === 0 ? t.replace(/^지금\s*-?\d+(\.\d+)?\s*℃\s*,?\s*/, '') : t))
		.filter((t) => t.trim().length > 0);
}

/** "마포구 서교동" (도 이름 뺀 행정구역) */
export function shortFull(full?: string, name?: string) {
	if (!full) return name ?? '';
	const parts = full.split(/\s+/);
	if (parts.length >= 3) return parts.slice(1).join(' ');
	return full;
}

/** 장소 이름 옆 보조 글자: 시군구 */
export function district(full?: string) {
	if (!full) return '';
	const parts = full.split(/\s+/);
	return parts.length >= 3 ? parts[parts.length - 2] : '';
}

/** 미세먼지 문구: "초미세먼지 나쁨". 등급이 없으면 '' */
export function airText(a: { grade: number; label: string; text: string } | null | undefined) {
	return a && a.grade > 0 && a.text ? `${a.label} ${a.text}` : '';
}

