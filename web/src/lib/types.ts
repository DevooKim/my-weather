// /api/timeline 응답 (docs/api-design-v1.md §3.3 + 실제 internal/timeline 구조)

export type Sky = 'clear' | 'partly' | 'cloudy';
export type Pty =
	| 'none'
	| 'rain'
	| 'rain_snow'
	| 'snow'
	| 'shower'
	| 'drizzle'
	| 'drizzle_snow'
	| 'flurry';

export interface Amount {
	mm: number;
	min: number;
	max: number | null;
	exact: boolean;
	text: string;
	code?: number;
}

export interface Slot {
	t: string;
	h: number;
	phase: 'past' | 'now' | 'future';
	res: '1h' | '3h';
	tmp: number | null;
	feels: number | null;
	sky: Sky | null;
	pty: Pty | null;
	wet: boolean;
	pcp: Amount | null;
	pop: number | null;
	reh: number | null;
	wsd: number | null;
	wsd_code?: number;
	vec: number | null;
	dir16: string | null;
	uuu: number | null;
	vvv: number | null;
	sno: Amount | null;
	/** 초단기예보 낙뢰. 0보다 크면 낙뢰 예상. 지금~6시간 칸에만 있다 */
	lgt?: number | null;
	src: { obs?: string; pcp?: string; sky?: string };
	estimated: boolean;
	partial: boolean;
}

export interface Source {
	kind: 'ncst' | 'ultra' | 'vilage';
	label: string;
	base?: string;
	delayed?: { expected_base: string };
}

export interface SummaryEvent {
	type: string;
	at?: string;
	pty?: Pty;
	pop?: number;
	tmp?: number;
	from_t?: string;
	to_t?: string;
	from?: number;
	to?: number;
	delta?: number;
}

export interface Summary {
	now: { t: string; tmp: number | null; pty: Pty | null; wet: boolean; sky: Sky | null; src?: string } | null;
	yesterday_same_hour: { t: string; tmp: number | null; src?: string; estimated: boolean } | null;
	diff_tmp: number | null;
	events: SummaryEvent[] | null;
	text: string[] | null;
}

export interface Day {
	date: string;
	label: string;
	tmin: number | null;
	tmax: number | null;
	sky: Sky | null;
	pty: Pty;
	wet: boolean;
	pop_max: number | null;
	pcp: Amount | null;
	estimated: boolean;
	complete: boolean;
	sun: { rise: string; set: string } | null;
	moon_phase: number;
}

export interface PlaceInfo {
	name?: string;
	full?: string;
	region?: string;
	nx: number;
	ny: number;
	lat: number;
	lon: number;
	fav_id?: number;
	archived?: boolean;
}

export interface Timeline {
	place: PlaceInfo;
	now: string;
	/** now 시각의 달 위상(0=삭, 0.5=망) */
	moon_phase?: number;
	range: { start: string; end: string; hourly_from: string; hourly_to: string };
	complete: boolean;
	retry_after_s: number | null;
	data_version: string;
	sources: Record<string, Source>;
	slots: Slot[];
	summary: Summary;
	daily: Day[] | null;
	/** 가까운 측정소의 지금 미세먼지(에어코리아). grade 1 좋음 · 2 보통 · 3 나쁨 · 4 매우나쁨 */
	air?: Air | null;
	/** 가까운 AWS 시정 관측소(15km 안)의 지금 안개 여부. 시정 1km 미만이거나 현천이 안개 */
	fog?: { station: number; at: string; km: number; vis: number; fog: boolean } | null;
	warnings: string[] | null;
}

/** 앱 안에서 쓰는 장소. region이 있으면 region이 정체성이다. */
export interface Place {
	name: string;
	full?: string;
	region?: string;
	nx: number;
	ny: number;
	/** 서버 즐겨찾기 id (로그인했을 때만) */
	id?: number;
	alerts?: { soon: boolean; eve: boolean };
}

export interface NowItem {
	nx: number;
	ny: number;
	tmp: number | null;
	sky?: Sky | null;
	pty?: Pty | null;
	observed_at?: string;
	pending?: boolean;
}

export interface Region {
	code: string;
	name: string;
	full: string;
	nx: number;
	ny: number;
	lat: number | null;
	lon: number | null;
}

export interface Air {
	station: string;
	at: string;
	pm10: number | null;
	pm25: number | null;
	pm10_grade: number | null;
	pm25_grade: number | null;
	grade: number;
	/** '미세먼지' 또는 '초미세먼지'(등급이 나쁜 쪽) */
	label: string;
	text: string;
}
