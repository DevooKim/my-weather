// 달 그림: moon.svg의 'basic' 테마를 옮겨 왔다(외부 요청 없이 여기서 만든다).
// https://github.com/hmu332233/moon.svg — server/theme/basic.ts
//
// MIT License
// Copyright (c) 2022 Minung Han
// Permission is hereby granted, free of charge, to any person obtaining a copy of this software and
// associated documentation files (the "Software"), to deal in the Software without restriction, including
// without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the
// following conditions: The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT
// LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO
// EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
// IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR
// THE USE OR OTHER DEALINGS IN THE SOFTWARE.

/** viewBox 320 안에서 달 지름은 300 */
export const MOON_VIEWBOX = 320;
export const MOON_DIAMETER = 300;

/**
 * 위상(0=삭, 0.5=망)으로 달 SVG 문자열을 만든다.
 * 원본과 같이 비친 비율 1% 미만(삭)이면 검은 원만, 1~5%는 5%로 올려 그린다.
 */
export function moonSvg(phase: number): string {
	const k = (1 - Math.cos(2 * Math.PI * phase)) / 2; // 비친 비율
	const waxing = phase < 0.5;
	const svg = (body: string) =>
		`<svg xmlns="http://www.w3.org/2000/svg" width="${MOON_VIEWBOX}" height="${MOON_VIEWBOX}" viewBox="0 0 320 320">${body}</svg>`;
	const dark = '<path d="m 160 10 a 20 20 0 1 1 0 300 a 20 20 0 1 1 0 -300" style="fill:#000;stroke:black;stroke-width:2"/>';
	if (k * 100 < 1) return svg(dark);
	const percent = Math.max(5, k * 100);
	const rx = Math.abs(((percent - 50) / 5) * 2);
	const half = percent < 50 ? 0 : 1;
	const [rx1, f1, rx2, f2] = waxing ? [20, 1, rx, half] : [rx, half, 20, 1];
	const lit = `<path d="m 160 10 a ${rx1} 20 0 1 ${f1} 0 300 a ${rx2} 20 0 1 ${f2} 0 -300" style="fill:#FEFCD7;stroke:black;stroke-width:4"/>`;
	return svg(dark + lit);
}
