import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, loadEnv } from 'vite';

// 개발할 때 /api를 넘길 곳: web/.env.local의 MW_API(예: 배포한 서버 주소). 없으면 로컬 서버.
// 프록시로는 상태를 바꾸는 요청이 Origin 검사로 403이 된다(읽기만 됨).
export default defineConfig(({ mode }) => {
	// @ts-ignore node 전역(타입 패키지 없이)
	const env = loadEnv(mode, process.cwd(), '');
	const target = env.MW_API || 'http://127.0.0.1:8080';
	return {
		plugins: [sveltekit()],
		server: {
			proxy: { '/api': { target, changeOrigin: true, secure: true } }
		}
	};
});
