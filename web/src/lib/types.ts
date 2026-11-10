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
