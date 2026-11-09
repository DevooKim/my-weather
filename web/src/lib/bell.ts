// 종 버튼: 두 알림(곧 비·전날 밤)을 함께 켜고 끈다 (§6)
import { app } from './app.svelte';
import { ensureSubscribed, needsInstall } from './push';
import type { Place } from './types';

export async function toggleBell(p: Place) {
	const f = app.favOf(p);
	if (!f) return;
	const on = !!f.alerts && (f.alerts.soon || f.alerts.eve);
	if (on) {
		await app.setAlerts(f, { soon: false, eve: false });
		return;
	}
	if (needsInstall()) {
		app.installOpen = true;
		return;
	}
	try {
		// 권한 요청은 누른 동작 안에서 바로 불러야 하므로 다른 await보다 먼저 둔다
		await ensureSubscribed();
	} catch (e) {
		app.showToast(e instanceof Error ? e.message : '알림을 켜지 못했어요.');
		return;
	}
	const r = await app.setAlerts(f, { soon: true, eve: true });
	if (r) app.showToast(`${f.name} 비 알림을 켰어요.`);
}
