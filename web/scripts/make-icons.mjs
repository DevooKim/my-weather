// 아이콘 PNG 만들기: scripts/icon.svg를 헤드리스 Chrome으로 찍는다. (한 번 만들어 static/에 커밋)
// 사용: CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" node scripts/make-icons.mjs
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const chrome = process.env.CHROME ?? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const svg = readFileSync(join(here, 'icon.svg'), 'utf8');
const dir = mkdtempSync(join(tmpdir(), 'mw-icon-'));
const out = resolve(here, '../static');

const jobs = [
	['icon-512.png', 512, 1],
	['icon-192.png', 192, 1],
	['apple-touch-icon.png', 180, 1],
	['favicon.png', 64, 1],
	// maskable: 안전 영역(80%) 안에 들어가게 줄인다
	['icon-maskable-512.png', 512, 0.78]
];
for (const [name, size, scale] of jobs) {
	const html = `<!doctype html><html><body style="margin:0;background:#2B3440;width:${size}px;height:${size}px;display:flex;align-items:center;justify-content:center;overflow:hidden">
<div style="width:${size * scale}px;height:${size * scale}px">${svg.replace('<svg ', `<svg width="${size * scale}" height="${size * scale}" `)}</div></body></html>`;
	const f = join(dir, name + '.html');
	writeFileSync(f, html);
	execFileSync(chrome, [
		'--headless=new',
		'--disable-gpu',
		'--hide-scrollbars',
		`--window-size=${size},${size}`,
		`--screenshot=${join(out, name)}`,
		'file://' + f
	], { stdio: 'ignore' });
	console.log('wrote', name);
}
