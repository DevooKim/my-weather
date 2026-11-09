/// <reference types="@sveltejs/kit" />
/// <reference no-default-lib="true"/>
/// <reference lib="esnext" />
/// <reference lib="webworker" />
// 앱 셸 캐시 + 푸시 알림. /api 응답은 캐시하지 않는다(앱이 localStorage에 마지막 타임라인을 둔다).
import { build, files, version } from '$service-worker';

const sw = self as unknown as ServiceWorkerGlobalScope;
const CACHE = `mw-shell-${version}`;
const SHELL = ['/', ...build, ...files.filter((f) => !f.endsWith('.DS_Store'))];

sw.addEventListener('install', (e) => {
	e.waitUntil(
		caches
			.open(CACHE)
			.then((c) => c.addAll(SHELL))
			.then(() => sw.skipWaiting())
	);
});

sw.addEventListener('activate', (e) => {
	e.waitUntil(
		(async () => {
			for (const k of await caches.keys()) if (k !== CACHE) await caches.delete(k);
			await sw.clients.claim();
		})()
	);
});

sw.addEventListener('fetch', (e) => {
	const req = e.request;
	if (req.method !== 'GET') return;
	const url = new URL(req.url);
	if (url.origin !== sw.location.origin) return;
	if (url.pathname.startsWith('/api/') || url.pathname === '/healthz') return;

	// 화면 이동: 네트워크 먼저, 안 되면 저장한 index.html (SPA)
	if (req.mode === 'navigate') {
		e.respondWith(
			(async () => {
				try {
					const res = await fetch(req);
					if (res.ok) (await caches.open(CACHE)).put('/', res.clone());
					return res;
				} catch {
					return (await caches.match('/')) ?? Response.error();
				}
			})()
		);
		return;
	}
	// 빌드 파일·아이콘: 캐시 먼저
	e.respondWith(
		(async () => {
			const hit = await caches.match(req);
			if (hit) return hit;
			const res = await fetch(req);
			if (res.ok && url.pathname.startsWith('/_app/immutable/')) (await caches.open(CACHE)).put(req, res.clone());
			return res;
		})()
	);
});

// 서버 payload: {"title","body","url":"/?fav=1","tag":"soon-1"|"eve-1"|"test"}
sw.addEventListener('push', (e) => {
	let d: { title?: string; body?: string; url?: string; tag?: string } = {};
	try {
		d = e.data?.json() ?? {};
	} catch {
		d = { body: e.data?.text() };
	}
	e.waitUntil(
		sw.registration.showNotification(d.title || 'my-weather', {
			body: d.body ?? '',
			tag: d.tag,
			icon: '/icon-192.png',
			badge: '/icon-192.png',
			data: { url: d.url || '/' }
		})
	);
});

sw.addEventListener('notificationclick', (e) => {
	e.notification.close();
	const target = new URL((e.notification.data?.url as string) || '/', sw.location.origin).href;
	e.waitUntil(
		(async () => {
			const list = await sw.clients.matchAll({ type: 'window', includeUncontrolled: true });
			for (const c of list) {
				if (new URL(c.url).origin === sw.location.origin) {
					await c.focus();
					if ('navigate' in c) await (c as WindowClient).navigate(target).catch(() => undefined);
					return;
				}
			}
			await sw.clients.openWindow(target);
		})()
	);
});
