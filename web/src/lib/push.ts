// Web Push 구독 (§2.7). VAPID 공개키는 서버에서 받는다.
import { api } from './api';

export function isStandalone() {
	return (
		matchMedia('(display-mode: standalone)').matches ||
		matchMedia('(display-mode: fullscreen)').matches ||
		navigator.standalone === true
	);
}

export function isIOS() {
	const ua = navigator.userAgent;
	return /iPhone|iPad|iPod/.test(ua) || (ua.includes('Macintosh') && navigator.maxTouchPoints > 1);
}

/** 이 기기에서 홈 화면 추가 안내가 필요한가 (iOS는 홈 화면 앱에서만 푸시 가능) */
export function needsInstall() {
	return isIOS() && !isStandalone();
}

export function pushSupported() {
	return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
}

export function deviceLabel() {
	const ua = navigator.userAgent;
	if (/iPhone/.test(ua)) return 'iPhone';
	if (/iPad/.test(ua) || (ua.includes('Macintosh') && navigator.maxTouchPoints > 1)) return 'iPad';
	if (/Android/.test(ua)) return 'Android';
	if (/Macintosh/.test(ua)) return 'Mac';
	if (/Windows/.test(ua)) return 'Windows';
	return '브라우저';
}

function b64urlToBytes(s: string) {
	const pad = '='.repeat((4 - (s.length % 4)) % 4);
	const raw = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
	const out = new Uint8Array(raw.length);
	for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
	return out;
}

async function registration() {
	const reg = (await navigator.serviceWorker.getRegistration('/')) ?? (await registerSW());
	if (!reg) throw new Error('서비스 워커를 등록하지 못했어요.');
	await navigator.serviceWorker.ready;
	return reg;
}

export async function registerSW() {
	if (!('serviceWorker' in navigator) || import.meta.env.DEV) return undefined;
	try {
		return await navigator.serviceWorker.register('/service-worker.js', { scope: '/' });
	} catch {
		return undefined;
	}
}

/** 지금 구독(없으면 null). 권한을 묻지 않는다. */
export async function currentSubscription() {
	if (!pushSupported()) return null;
	const reg = await navigator.serviceWorker.getRegistration('/');
	return (await reg?.pushManager.getSubscription()) ?? null;
}

/**
 * 알림 권한을 받고 구독해서 서버에 등록한다. 사용자 동작(버튼 누름) 안에서 불러야 한다.
 * 반환: 구독 endpoint
 */
export async function ensureSubscribed(): Promise<string> {
	if (!pushSupported()) throw new Error('이 브라우저는 알림을 지원하지 않아요.');
	const perm = Notification.permission === 'default' ? await Notification.requestPermission() : Notification.permission;
	if (perm !== 'granted') throw new Error('알림 권한이 꺼져 있어요. 설정에서 이 앱의 알림을 허용해 주세요.');
	const reg = await registration();
	let sub = await reg.pushManager.getSubscription();
	if (!sub) {
		const { key } = await api.vapidKey();
		sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64urlToBytes(key) });
	}
	// 같은 endpoint면 서버가 키만 갱신한다(멱등). 매번 보내서 서버에서 지워진 구독도 되살린다.
	await api.subscribe(sub.toJSON(), deviceLabel());
	return sub.endpoint;
}
