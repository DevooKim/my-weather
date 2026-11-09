import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

// 개발할 때 /api는 배포된 공개 API로 넘긴다(읽기용). 바꾸려면 MW_API=http://127.0.0.1:8080
// @ts-ignore node 전역(타입 패키지 없이)
const target = process.env.MW_API ?? 'https://weather.example.com';

export default defineConfig({
	plugins: [sveltekit()],
	server: {
		proxy: { '/api': { target, changeOrigin: true, secure: true } }
	}
});
