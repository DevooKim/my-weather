<script lang="ts">
	// 배경 모션: 비·눈(세기=강수량, 기울기=바람), 해·달(위상), 구름(구름 많음일 때만, 바람 방향으로 천천히 흐름),
	// 번개(낙뢰 예보: 몇 초에 한 번 약하게 번쩍), 안개(옆으로 흐르는 옅은 띠), 황사(누런 기운과 고운 먼지).
	// prefers-reduced-motion이면 움직이지 않는 한 장면만 그린다.
	import type { Scene } from '$lib/scene';
	import { MOON_DIAMETER, MOON_VIEWBOX, moonSvg } from '$lib/moon';
	import { onMount } from 'svelte';

	// height: 고정 높이(px). 스크롤로 접히는 머리처럼 부모 높이가 매 프레임 바뀌는 곳에서는
	// 캔버스를 가장 큰 높이로 한 번만 만들고 부모가 잘라 보이게 한다(크기 재설정·입자 리셋이 없어 부드럽다).
	// maxDpr: 화면 전체에 까는 큰 캔버스(PC 배경)는 1로 낮춰 그리는 양을 줄인다. 부드러운 장면이라 차이가 거의 안 보인다
	let { scene, height, maxDpr = 2 }: { scene: Scene; height?: number; maxDpr?: number } = $props();

	let canvas: HTMLCanvasElement | undefined = $state();

	// 달(밤·맑음/구름 조금): 반지름 18px에 맞춘 그림 크기. 위치는 캔버스의 해·달 자리와 같다
	const MOON_R = 18;
	const moonSize = ((2 * MOON_R) / MOON_DIAMETER) * MOON_VIEWBOX;
	const showMoon = $derived(scene.light === 'night' && (scene.kind === 'clear' || scene.kind === 'partly'));
	const moon = $derived(showMoon ? moonSvg(scene.moonPhase) : '');

	type P = { x: number; y: number; z: number; s: number };
	/** 구름 하나: 미리 그려 둔 그림(sp)을 x로 흘린다. z가 클수록 가깝다(크고 빠름) */
	type C = { x: number; y: number; z: number; sp: HTMLCanvasElement; sw: number; sh: number };
	/** 안개 띠 하나 */
	type F = { x: number; y: number; w: number; h: number; v: number; a: number };

	onMount(() => {
		const cv = canvas!;
		const ctx = cv.getContext('2d');
		if (!ctx) return;
		const mq = matchMedia('(prefers-reduced-motion: reduce)');
		let w = 0,
			h = 0,
			dpr = 1,
			raf = 0,
			last = 0,
			visible = true,
			parts: P[] = [],
			clouds: C[] = [],
			dusts: P[] = [],
			fogs: F[] = [],
			flashT = -1, // 번개가 친 뒤 지난 초(-1 = 없음)
			nextFlash = 3,
			bolt: { pts: [number, number][]; width: number }[] = [];

		const count = () => {
			const sc = scene;
			if (sc.rate <= 0) return 0;
			const area = (w * h) / (390 * 430);
			const base = sc.kind === 'snow' ? 40 + sc.rate * 60 : 30 + sc.rate * 45;
			return Math.round(Math.min(sc.kind === 'snow' ? 220 : 320, base) * Math.max(0.5, area));
		};
		const spawn = (anyY: boolean): P => ({
			x: Math.random() * (w + 200) - 100,
			y: anyY ? Math.random() * h : -20 - Math.random() * 40,
			z: 0.5 + Math.random() * 0.5,
			s: Math.random() * Math.PI * 2
		});
		const reset = () => {
			const n = count();
			parts = Array.from({ length: n }, () => spawn(true));
			resetClouds();
			resetFogDust();
			flashT = -1;
			nextFlash = 1.5 + Math.random() * 2;
		};
		const animated = () => parts.length > 0 || clouds.length > 0 || dusts.length > 0 || fogs.length > 0 || scene.lightning;

		// --- 안개·황사 ---
		function resetFogDust() {
			if (!w || !h) return;
			const sc = scene;
			fogs = sc.fog
				? Array.from({ length: 5 }, (_, i) => ({
						x: Math.random() * w * 1.4 - w * 0.6,
						y: h * (0.15 + i * 0.17) + (Math.random() - 0.5) * h * 0.06,
						w: w * (0.9 + Math.random() * 0.7),
						h: h * (0.16 + Math.random() * 0.1),
						v: (6 + Math.random() * 8) * (i % 2 ? -1 : 1),
						a: 0.1 + Math.random() * 0.08
					}))
				: [];
			const area = (w * h) / (390 * 430);
			dusts = sc.dust ? Array.from({ length: Math.round(90 * Math.max(0.5, area)) }, () => spawn(true)) : [];
		}
		function drawFog(dt: number) {
			if (!fogs.length) return;
			const c = ctx!;
			// 안개는 전체를 옅게 흐리게 덮고, 그 위로 더 진한 띠가 천천히 흐른다
			c.fillStyle = 'rgba(210,216,224,0.12)';
			c.fillRect(0, 0, w, h);
			for (const f of fogs) {
				f.x += f.v * dt;
				if (f.v > 0 && f.x > w) f.x = -f.w;
				if (f.v < 0 && f.x < -f.w) f.x = w;
				const cx = f.x + f.w / 2,
					cy = f.y;
				c.save();
				c.translate(cx, cy);
				c.scale(1, f.h / f.w);
				const g = c.createRadialGradient(0, 0, 0, 0, 0, f.w / 2);
				g.addColorStop(0, `rgba(225,230,236,${f.a})`);
				g.addColorStop(1, 'rgba(225,230,236,0)');
				c.fillStyle = g;
				c.beginPath();
				c.arc(0, 0, f.w / 2, 0, Math.PI * 2);
				c.fill();
				c.restore();
			}
		}
		function drawDust(dt: number) {
			if (!scene.dust) return;
			const c = ctx!;
			// 누런 기운: 아래로 갈수록 진하게
			const g = c.createLinearGradient(0, 0, 0, h);
			g.addColorStop(0, 'rgba(196,150,80,0.16)');
			g.addColorStop(1, 'rgba(176,128,64,0.30)');
			c.fillStyle = g;
			c.fillRect(0, 0, w, h);
			const vx = Math.abs(scene.u) < 0.5 ? 10 : Math.max(-12, Math.min(12, scene.u)) * 6;
			for (const p of dusts) {
				p.s += dt * 0.8;
				p.x += (vx + Math.sin(p.s) * 6) * p.z * dt;
				p.y += (4 + Math.cos(p.s) * 5) * dt;
				if (p.x > w + 10) p.x = -10;
				if (p.x < -10) p.x = w + 10;
				if (p.y > h + 10) p.y = -10;
				c.fillStyle = `rgba(232,200,140,${0.25 + 0.3 * p.z})`;
				c.beginPath();
				c.arc(p.x, p.y, 0.6 + 1.1 * p.z, 0, Math.PI * 2);
				c.fill();
			}
		}

		// --- 번개 ---
		// 줄기: 위에서 아래로 지그재그(큰 꺾임 + 잔 떨림)로 내려가고, 중간중간 가는 가지가 옆으로 뻗는다.
		// 깜빡임이 불편하지 않게: 4~9초에 한 번, 1초 안에 밝은 순간은 두 번뿐(초당 3회 미만), 0.7초 안에 사라진다.
		type Seg = { pts: [number, number][]; width: number };
		function zigzag(x: number, y: number, len: number, dir: number, jag: number): [number, number][] {
			const pts: [number, number][] = [[x, y]];
			const n = Math.max(6, Math.round(len / 14));
			let drift = dir;
			for (let i = 0; i < n; i++) {
				drift += (Math.random() - 0.5) * 0.6;
				const step = len / n;
				x += drift * step * 0.5 + (Math.random() - 0.5) * jag;
				y += step * (0.7 + Math.random() * 0.6);
				pts.push([x, y]);
			}
			return pts;
		}
		function makeBolt(): Seg[] {
			const x0 = w * (0.2 + Math.random() * 0.6);
			const len = h * (0.55 + Math.random() * 0.3);
			const main = zigzag(x0, -10, len, (Math.random() - 0.5) * 0.6, 22);
			const segs: Seg[] = [{ pts: main, width: 1 }];
			const nb = 2 + Math.floor(Math.random() * 3);
			for (let i = 0; i < nb; i++) {
				const at = main[1 + Math.floor(Math.random() * (main.length * 0.6))];
				const side = Math.random() < 0.5 ? -1 : 1;
				const b = zigzag(at[0], at[1], len * (0.15 + Math.random() * 0.25), side * (0.8 + Math.random()), 14);
				segs.push({ pts: b, width: 0.45 });
				// 가지의 잔가지
				if (Math.random() < 0.5) {
					const at2 = b[Math.floor(b.length / 2)];
					segs.push({ pts: zigzag(at2[0], at2[1], len * 0.1, side * 1.4, 10), width: 0.25 });
				}
			}
			return segs;
		}
		let boltAt = 0.5; // 번개 친 곳(가로 비율): 하늘 밝아지는 중심
		function drawLightning(dt: number) {
			if (!scene.lightning) return;
			const c = ctx!;
			if (flashT < 0) {
				nextFlash -= dt;
				if (nextFlash <= 0) {
					flashT = 0;
					// 75%는 줄기가 보이고, 나머지는 구름 속 번개(하늘만 번쩍)
					bolt = Math.random() < 0.75 ? makeBolt() : [];
					boltAt = bolt.length ? bolt[0].pts[0][0] / Math.max(1, w) : 0.2 + Math.random() * 0.6;
					nextFlash = 4 + Math.random() * 5;
				}
				return;
			}
			flashT += dt;
			const t = flashT;
			// 번쩍(0~0.07) → 잠깐 어두움 → 다시 번쩍(0.15~0.22) → 0.7초까지 사그라듦
			const pulse = t < 0.07 ? 1 : t < 0.15 ? 0.25 : t < 0.22 ? 0.9 : Math.max(0, 0.9 * (1 - (t - 0.22) / 0.48));
			if (pulse <= 0) {
				flashT = -1;
				return;
			}
			// 하늘: 전체가 살짝 + 번개 친 쪽 위가 더 밝게
			c.fillStyle = `rgba(215,225,255,${0.14 * pulse})`;
			c.fillRect(0, 0, w, h);
			const gx = boltAt * w;
			const g = c.createRadialGradient(gx, 0, 0, gx, 0, Math.max(w, h) * 0.8);
			g.addColorStop(0, `rgba(235,240,255,${0.42 * pulse})`);
			g.addColorStop(1, 'rgba(235,240,255,0)');
			c.fillStyle = g;
			c.fillRect(0, 0, w, h);
			if (!bolt.length || t > 0.45) return;
			// 줄기: 넓은 빛 번짐 → 푸른 테 → 흰 심 순으로 겹쳐 그린다
			const a = t < 0.07 || (t > 0.15 && t < 0.22) ? 1 : 0.55 * pulse;
			const base = Math.max(1.6, Math.min(w, 520) / 160);
			c.save();
			c.lineJoin = 'round';
			c.lineCap = 'round';
			const pass = (width: number, color: string, blur: number) => {
				c.strokeStyle = color;
				c.shadowColor = 'rgba(170,195,255,0.95)';
				c.shadowBlur = blur;
				for (const sgm of bolt) {
					c.lineWidth = width * sgm.width;
					c.beginPath();
					sgm.pts.forEach(([x, y], i) => (i ? c.lineTo(x, y) : c.moveTo(x, y)));
					c.stroke();
				}
			};
			pass(base * 5, `rgba(150,180,255,${0.22 * a})`, 28);
			pass(base * 2.2, `rgba(205,220,255,${0.7 * a})`, 14);
			pass(base, `rgba(255,255,255,${a})`, 6);
			c.restore();
		}

		// 구름: 적운 모양. 가운데 큰 봉우리와 양옆 어깨를 겹치고, 바닥은 평평하게 잘라 아래로 흐려지게 한다.
		// 미리 한 장으로 그려 두고 프레임마다 drawImage만 한다.
		function cloudSprite(cw: number): { sp: HTMLCanvasElement; sw: number; sh: number } {
			const ch = cw * 0.55;
			const sp = document.createElement('canvas');
			sp.width = Math.ceil(cw * dpr);
			sp.height = Math.ceil(ch * dpr);
			const c = sp.getContext('2d')!;
			c.scale(dpr, dpr);
			const rnd = (a: number, b: number) => a + Math.random() * (b - a);
			const puff = (x: number, y: number, r: number) => {
				const g = c.createRadialGradient(x, y - r * 0.15, r * 0.55, x, y, r);
				g.addColorStop(0, 'rgba(255,255,255,1)');
				g.addColorStop(1, 'rgba(255,255,255,0)');
				c.fillStyle = g;
				c.beginPath();
				c.arc(x, y, r, 0, Math.PI * 2);
				c.fill();
			};
			const base = ch * 0.78; // 바닥선
			// 봉우리: 가운데(가장 큼)를 조금 한쪽으로 치우치게 하고, 양옆으로 갈수록 작고 낮게
			const peak = rnd(0.42, 0.58);
			const domes: [number, number][] = [
				[peak, rnd(0.4, 0.46)],
				[peak - rnd(0.2, 0.26), rnd(0.28, 0.34)],
				[peak + rnd(0.2, 0.26), rnd(0.26, 0.32)],
				[peak - rnd(0.36, 0.42), rnd(0.16, 0.2)],
				[peak + rnd(0.36, 0.42), rnd(0.15, 0.19)]
			];
			for (const [fx, fr] of domes) {
				const r = ch * fr;
				// 가운데로 모아 가장자리 덩이가 그림 밖으로 잘리지 않게
				puff(cw * (0.5 + (fx - 0.5) * 0.75), base - r * 0.55, r);
			}
			// 몸통을 채우는 낮고 넓은 덩이
			for (let i = 0; i < 4; i++) puff(cw * (0.22 + i * 0.19), base - ch * 0.12, ch * 0.2);
			// 바닥: 아래쪽을 평평하게 자르고 살짝 흐리게
			c.globalCompositeOperation = 'destination-in';
			const m = c.createLinearGradient(0, 0, 0, ch);
			m.addColorStop(0, 'rgba(0,0,0,1)');
			m.addColorStop(base / ch - 0.08, 'rgba(0,0,0,1)');
			m.addColorStop(base / ch + 0.04, 'rgba(0,0,0,0)');
			c.fillStyle = m;
			c.fillRect(0, 0, cw, ch);
			// 아랫부분을 조금 어둡게(입체감)
			c.globalCompositeOperation = 'source-atop';
			const sh = c.createLinearGradient(0, ch * 0.3, 0, base);
			sh.addColorStop(0, 'rgba(0,0,0,0)');
			sh.addColorStop(1, 'rgba(60,70,90,0.35)');
			c.fillStyle = sh;
			c.fillRect(0, 0, cw, ch);
			c.globalCompositeOperation = 'source-over';
			return { sp, sw: cw, sh: ch };
		}
		const cloudCount = () => {
			if (scene.kind !== 'partly') return 0;
			return Math.max(2, Math.round(3 * Math.min(1.6, Math.max(0.6, w / 390))));
		};
		const spawnCloud = (anyX: boolean): C => {
			const z = 0.4 + Math.random() * 0.6;
			const base = Math.min(w, 520);
			const spr = cloudSprite(base * (0.35 + 0.35 * z));
			const dir = cloudVx() >= 0 ? 1 : -1;
			return {
				z,
				...spr,
				x: anyX ? Math.random() * (w + spr.sw) - spr.sw : dir > 0 ? -spr.sw : w,
				y: Math.random() * Math.min(h * 0.45, 260) - spr.sh * 0.3
			};
		};
		function resetClouds() {
			if (!w || !h) return;
			clouds = Array.from({ length: cloudCount() }, () => spawnCloud(true)).sort((a, b) => a.z - b.z);
		}
		/** 구름 속도(px/s): 동서 바람을 따르되 아주 느리게. 바람이 없으면 오른쪽으로 살짝 */
		const cloudVx = () => {
			const u = Math.max(-12, Math.min(12, scene.u));
			return Math.abs(u) < 0.5 ? 5 : u * 3;
		};
		function drawClouds(dt: number) {
			if (!clouds.length) return;
			const sc = scene;
			const night = sc.light === 'night';
			// 밤에는 어둡게(흰 구름 위에 색을 덮는 대신 투명도를 낮춘다)
			const a = 0.26 * (night ? 0.6 : 1);
			const vx = cloudVx();
			const c = ctx!;
			for (let i = 0; i < clouds.length; i++) {
				const cl = clouds[i];
				cl.x += vx * (0.5 + cl.z) * dt;
				if (vx > 0 && cl.x > w) Object.assign(cl, spawnCloud(false));
				else if (vx < 0 && cl.x < -cl.sw) Object.assign(cl, spawnCloud(false));
				c.globalAlpha = a * (0.6 + 0.4 * cl.z);
				c.drawImage(cl.sp, cl.x, cl.y, cl.sw, cl.sh);
			}
			c.globalAlpha = 1;
		}
		const resize = () => {
			const r = cv.getBoundingClientRect();
			const nd = Math.min(maxDpr, window.devicePixelRatio || 1);
			const nw = Math.round(r.width),
				nh = Math.round(r.height);
			if (nw === w && nh === h && nd === dpr) return;
			const first = w === 0 || h === 0;
			const ow = w,
				oh = h;
			dpr = nd;
			w = nw;
			h = nh;
			cv.width = Math.round(w * dpr);
			cv.height = Math.round(h * dpr);
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			if (first) reset();
			else {
				resetClouds();
				resetFogDust();
				// 입자를 버리지 않고 새 크기에 맞춰 옮긴 뒤 개수만 맞춘다(크기가 바뀌어도 비가 튀지 않게)
				for (const p of parts) {
					p.x = (p.x / Math.max(1, ow)) * w;
					p.y = (p.y / Math.max(1, oh)) * h;
				}
				const n = count();
				if (parts.length > n) parts.length = n;
				while (parts.length < n) parts.push(spawn(true));
			}
			if (mq.matches) draw(0);
		};

		function drawSky() {
			const sc = scene;
			if (sc.kind === 'rain' || sc.kind === 'snow' || sc.kind === 'mixed' || sc.kind === 'cloudy') return;
			const x = w - Math.min(90, w * 0.22),
				y = Math.min(80, h * 0.2);
			if (sc.light === 'day' || sc.light === 'dawn' || sc.light === 'dusk') {
				const r = 26;
				const g = ctx!.createRadialGradient(x, y, r * 0.6, x, y, r * 3.2);
				g.addColorStop(0, sc.light === 'day' ? 'rgba(255,236,170,0.35)' : 'rgba(255,190,140,0.35)');
				g.addColorStop(1, 'rgba(255,236,170,0)');
				ctx!.fillStyle = g;
				ctx!.beginPath();
				ctx!.arc(x, y, r * 3.2, 0, Math.PI * 2);
				ctx!.fill();
				ctx!.fillStyle = sc.light === 'day' ? 'rgba(255,226,140,0.9)' : 'rgba(255,190,130,0.85)';
				ctx!.beginPath();
				ctx!.arc(x, y + (sc.light === 'day' ? 0 : h * 0.35), r, 0, Math.PI * 2);
				ctx!.fill();
			}
			// 밤에는 달: 캔버스가 아니라 아래 .moon(SVG)로 그린다. 캔버스 해상도를 낮춰도(PC 배경) 달은 선명하게
		}

		function draw(dt: number) {
			const sc = scene;
			ctx!.clearRect(0, 0, w, h);
			drawSky();
			drawClouds(dt);
			drawPrecip(dt);
			drawDust(dt);
			drawFog(dt);
			drawLightning(dt);
		}

		function drawPrecip(dt: number) {
			const sc = scene;
			if (!parts.length) return;
			const snow = sc.kind === 'snow';
			const mixed = sc.kind === 'mixed';
			// 바람: 동쪽으로 부는 성분(u)이 화면 오른쪽 기울기. 남북 성분(v)은 화면 안팎이라 기울기 대신 세기에 조금 보탠다.
			const vx = Math.max(-14, Math.min(14, sc.u)) * (snow ? 14 : 38);
			const vyRain = 760 + Math.min(10, Math.hypot(sc.u, sc.v)) * 15;
			ctx!.lineCap = 'round';
			for (let i = 0; i < parts.length; i++) {
				const p = parts[i];
				const isFlake = snow || (mixed && i % 2 === 0);
				if (isFlake) {
					p.s += dt * 1.5;
					p.x += (vx + Math.sin(p.s) * 18) * p.z * dt;
					p.y += (45 + 35 * p.z) * dt;
					ctx!.fillStyle = `rgba(255,255,255,${0.35 + 0.4 * p.z})`;
					ctx!.beginPath();
					ctx!.arc(p.x, p.y, 1.2 + 1.6 * p.z, 0, Math.PI * 2);
					ctx!.fill();
				} else {
					const vy = vyRain * p.z;
					p.x += vx * p.z * dt;
					p.y += vy * dt;
					const len = 14 + 8 * p.z;
					const dx = (vx / vyRain) * len,
						dy = len;
					ctx!.strokeStyle = `rgba(255,255,255,${0.12 + 0.18 * p.z})`;
					ctx!.lineWidth = 1 + 0.6 * p.z;
					ctx!.beginPath();
					ctx!.moveTo(p.x, p.y);
					ctx!.lineTo(p.x - dx, p.y - dy);
					ctx!.stroke();
				}
				if (p.y > h + 30 || p.x < -120 || p.x > w + 120) Object.assign(p, spawn(false));
			}
		}

		function frame(t: number) {
			raf = 0;
			if (!visible || mq.matches) return;
			const dt = last ? Math.min(0.05, (t - last) / 1000) : 0.016;
			last = t;
			draw(dt);
			if (animated()) raf = requestAnimationFrame(frame);
		}
		const start = () => {
			if (raf || mq.matches) {
				if (mq.matches) draw(0);
				return;
			}
			last = 0;
			if (animated()) raf = requestAnimationFrame(frame);
			else draw(0);
		};
		const stop = () => {
			if (raf) cancelAnimationFrame(raf);
			raf = 0;
		};

		const ro = new ResizeObserver(() => {
			resize();
			start();
		});
		ro.observe(cv);
		const io = new IntersectionObserver((es) => {
			visible = es[0]?.isIntersecting ?? true;
			visible ? start() : stop();
		});
		io.observe(cv);
		const onVis = () => (document.hidden ? stop() : start());
		document.addEventListener('visibilitychange', onVis);
		const onMq = () => {
			stop();
			start();
		};
		mq.addEventListener('change', onMq);

		// 장면이 바뀌면 입자 수를 다시 맞춘다
		const unwatch = $effect.root(() => {
			$effect(() => {
				void scene.kind;
				void scene.rate;
				void scene.light;
				void scene.moonPhase;
				void scene.lightning;
				void scene.fog;
				void scene.dust;
				reset();
				stop();
				start();
			});
		});

		return () => {
			stop();
			ro.disconnect();
			io.disconnect();
			document.removeEventListener('visibilitychange', onVis);
			mq.removeEventListener('change', onMq);
			unwatch();
		};
	});
</script>

<div
	class="bg"
	class:fixedh={height != null}
	style="background: linear-gradient(180deg, {scene.top} 0%, {scene.bottom} 100%);{height != null ? ` height:${height}px;` : ''}"
>
	{#if moon}
		<!-- 캔버스보다 먼저 두어 구름·안개가 달 위로 지나간다 -->
		<div class="moon" style="width:{moonSize}px;height:{moonSize}px" aria-hidden="true">{@html moon}</div>
	{/if}
	<canvas bind:this={canvas} aria-hidden="true"></canvas>
</div>

<style>
	.bg {
		position: absolute;
		inset: 0;
		overflow: hidden;
		transition: background 0.6s;
	}
	.bg.fixedh {
		bottom: auto;
	}
	.moon {
		position: absolute;
		right: min(90px, 22%);
		top: min(80px, 20%);
		transform: translate(50%, -50%);
		pointer-events: none;
	}
	.moon :global(svg) {
		display: block;
		width: 100%;
		height: 100%;
	}
	canvas {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		display: block;
	}
</style>
