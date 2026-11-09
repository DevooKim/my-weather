// web/build -> internal/webui/dist (go:embed는 패키지 디렉터리 아래만 넣을 수 있다)
import { cpSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const src = resolve(here, '../build');
const dst = resolve(here, '../../internal/webui/dist');
if (!existsSync(resolve(src, 'index.html'))) {
	console.error('copy-dist: build/index.html 없음');
	process.exit(1);
}
rmSync(dst, { recursive: true, force: true });
// Docker web 단계처럼 internal/webui가 없는 곳에서도 돌게
mkdirSync(dirname(dst), { recursive: true });
cpSync(src, dst, { recursive: true });
console.log(`copy-dist: ${src} -> ${dst}`);
