// 화면 모드. 첫 페인트 전에 app.html의 인라인 스크립트가 같은 키로 적용한다.
export type ThemePref = 'system' | 'light' | 'dark';

export const THEME_KEY = 'mw.theme';

export function readTheme(): ThemePref {
	try {
		const v = localStorage.getItem(THEME_KEY);
		if (v === 'light' || v === 'dark' || v === 'system') return v;
	} catch {
		/* 사파리 비공개 모드 등 */
	}
	return 'system';
}

export function resolvedTheme(pref: ThemePref = readTheme()): 'light' | 'dark' {
	if (pref === 'light' || pref === 'dark') return pref;
	return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function applyTheme(pref: ThemePref = readTheme()) {
	const mode = resolvedTheme(pref);
	document.documentElement.dataset.theme = mode;
	document.documentElement.style.colorScheme = mode;
	document.querySelector('meta[name="color-scheme"]')?.setAttribute('content', mode);
}

export function setTheme(pref: ThemePref) {
	try {
		localStorage.setItem(THEME_KEY, pref);
	} catch {
		/* 저장이 막혀도 이번 세션에는 적용 */
	}
	applyTheme(pref);
}

let watching = false;
export function watchTheme() {
	if (watching) return;
	watching = true;
	applyTheme();
	matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
		if (readTheme() === 'system') applyTheme('system');
	});
}
