// 첫 화면 머리 글자(모바일 Hero, PC 하늘 머리가 같이 쓴다): 지금 기온과 한 줄 설명
import { airText, deg, nowPrecipText, weatherWord } from './format';
import type { Timeline } from './types';

export function heroText(tl: Timeline | null) {
	const nowSlot = tl?.slots.find((s) => s.phase === 'now') ?? null;
	const tmp = tl?.summary.now?.tmp ?? nowSlot?.tmp ?? null;
	let word = '';
	if (nowSlot) {
		const sky = nowSlot.sky ?? tl?.slots.find((s) => s.phase === 'future' && s.sky)?.sky ?? null;
		word = weatherWord({ ...nowSlot, sky });
	}
	const precip = nowSlot ? nowPrecipText(nowSlot) : '';
	const feels =
		nowSlot?.feels != null && tmp != null && Math.round(nowSlot.feels) !== Math.round(tmp) ? `체감 ${deg(nowSlot.feels)}` : '';
	// 미세먼지는 나쁨 이상일 때만 머리에 붙인다(좋음·보통은 상세 패널에서)
	const airBad = (tl?.air?.grade ?? 0) >= 3 ? airText(tl?.air) : '';
	const fogWord = tl?.fog?.fog ? '안개' : '';
	/** 큰 머리용: 강수(시간당) 또는 날씨 · 안개 · 체감 · 나쁜 공기 */
	const desc = [precip ? precip.replace(/^(\S+) /, '$1 · 시간당 ') : word, fogWord, feels, airBad].filter(Boolean).join(' · ');
	/** 접힌 한 줄용 */
	const short = precip || word;
	return { tmp, desc, short };
}
