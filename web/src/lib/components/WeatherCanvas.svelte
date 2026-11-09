<script lang="ts">
	// 배경 모션: 비·눈(세기=강수량, 기울기=바람), 해·달(위상). 구름 모션 없음.
	// prefers-reduced-motion이면 움직이지 않는 한 장면만 그린다.
	import type { Scene } from '$lib/scene';
	import { onMount } from 'svelte';

	// height: 고정 높이(px). 스크롤로 접히는 머리처럼 부모 높이가 매 프레임 바뀌는 곳에서는
	// 캔버스를 가장 큰 높이로 한 번만 만들고 부모가 잘라 보이게 한다(크기 재설정·입자 리셋이 없어 부드럽다).
	let { scene, height }: { scene: Scene; height?: number } = $props();

	let canvas: HTMLCanvasElement | undefined = $state();

	type P = { x: number; y: number; z: number; s: number };

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
			parts: P[] = [];

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
		};
		const resize = () => {
			const r = cv.getBoundingClientRect();
			const nd = Math.min(2, window.devicePixelRatio || 1);
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
			} else {
				drawMoon(x, y, 18, sc.moonPhase);
			}
		}

		// 달 위상: 0=삭, 0.5=망. 밝은 부분을 두 호로 그린다.
		function drawMoon(x: number, y: number, r: number, ph: number) {
			const c = ctx!;
			c.save();
			c.fillStyle = 'rgba(255,255,255,0.10)';
			c.beginPath();
			c.arc(x, y, r, 0, Math.PI * 2);
			c.fill();
			const lit = (1 - Math.cos(2 * Math.PI * ph)) / 2; // 비친 비율
			if (lit > 0.02) {
				const waxing = ph < 0.5;
				const k = Math.cos(Math.PI * lit); // 터미네이터 타원 x 반지름 비율 (1→-1)
				c.fillStyle = 'rgba(244,241,228,0.92)';
				c.beginPath();
				// 바깥 반원 (차는 달은 오른쪽이 밝다)
				c.arc(x, y, r, -Math.PI / 2, Math.PI / 2, !waxing);
				// 터미네이터
				c.ellipse(x, y, Math.abs(k) * r, r, 0, Math.PI / 2, -Math.PI / 2, waxing ? k > 0 : k < 0);
				c.closePath();
				c.fill();
			}
			c.restore();
		}

		function draw(dt: number) {
			const sc = scene;
			ctx!.clearRect(0, 0, w, h);
			drawSky();
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
			if (parts.length) raf = requestAnimationFrame(frame);
		}
		const start = () => {
			if (raf || mq.matches) {
				if (mq.matches) draw(0);
				return;
			}
			last = 0;
			if (parts.length) raf = requestAnimationFrame(frame);
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
	canvas {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		display: block;
	}
</style>
