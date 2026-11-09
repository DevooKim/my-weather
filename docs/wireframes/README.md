# 확정 와이어프레임 (원본 캔버스: https://claude.ai/artifact/3GjibGTdTNb5W84h4zgKVH)

각 파일은 Design 캔버스의 아트보드 소스(`.dc.html`)다. 레이아웃·문구·크기 참고용이고, 그대로 실행되는 페이지는 아니다(`{{...}}`와 `<sc-for>`는 캔버스 템플릿 문법). 날씨 값은 예시.

| 파일 | 내용 | 확정 |
|---|---|---|
| Main.dc.html | 1A: 위쪽 절반 큰 현재 날씨 + 배경 모션, 아래 타임라인 | 첫 화면 |
| A3-detail.dc.html | 1C: 스크롤 후 접힌 크기 | 1A→1C로 접힘 |
| E1-collapsed.dc.html | 5A: 접힌 화면 + 상세 패널(기본 '지금') | ✓ |
| E2-daily.dc.html | 5B: 더 내리면 일별 요약(머리 한 줄 고정) | ✓ |
| Timeline.dc.html | 타임라인 부품(실측 회색 배경, 지금 세로선, 1시간 파란 띠) | ✓ |
| C2-panel.dc.html | 3B: 그래프 아래 고정 상세 패널 + '지금으로' | ✓ |
| B2-sheet.dc.html | 2B: 장소 이름 누르면 아래 시트(검색, 현재 위치, 즐겨찾기, 최근) | ✓ |
| E3-sheet-admin.dc.html | 5C: 로그인 상태 시트(별, 종, 끌어서 순서, 밀어서 삭제) | ✓ |
| D2-two-col.dc.html | 4B: 데스크톱 2단 | ✓ |
| E4-settings.dc.html | 5D: 설정 | ✓ |
| E5-login.dc.html | 5E: 로그인·패스키 첫 등록 | ✓ |
| E6-states.dc.html | 5F: 오류·빈 상태 | ✓ |
| E7-install.dc.html | 5G: 홈 화면 추가 안내 | ✓ |
