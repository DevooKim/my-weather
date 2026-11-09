// Command gen은 기상청 격자·위경도 xlsx(활용가이드 부록)를 internal/grid/regions.csv로 바꾼다.
//
// 원본: "기상청41_단기예보 조회서비스_오픈API활용가이드_격자_위경도(2607).xlsx"
// (활용가이드 2609판 묶음, sha256 746b5e5be10430106abccc795a16c16d0f1fd0f081e8ab2765d0adc983a003c1).
// 3,838행 → 고유 격자 1,632개. 이어도 2행은 대표 좌표가 0이라 위경도 없이 둔다.
//
// 표준 라이브러리(archive/zip, encoding/xml)만 쓴다. 내려받은 파일은 새 빈 폴더로 복사한 뒤
// 저장소 루트에서 경로를 인수로 넘겨 실행한다(그 폴더 안에서 실행하지 않는다).
//
//	go run ./internal/grid/gen -in /path/to/copy/grid.xlsx -out internal/grid/regions.csv
//	go run ./internal/grid/gen -in ... -dump 5   # 앞 5행의 원본 셀을 출력(구조 확인용)
package main

import (
	"archive/zip"
	"encoding/csv"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)

type sst struct {
	SI []struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

type sheet struct {
	Rows []struct {
		C []struct {
			R  string `xml:"r,attr"`
			T  string `xml:"t,attr"`
			V  string `xml:"v"`
			IS struct {
				T string `xml:"t"`
			} `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

const maxEntry = 64 << 20 // 압축 해제 크기 상한 (zip bomb 방지)

func readEntry(zr *zip.ReadCloser, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			if f.UncompressedSize64 > maxEntry {
				return nil, fmt.Errorf("%s too large", name)
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxEntry))
		}
	}
	return nil, fmt.Errorf("%s not found", name)
}

// col은 "AB12" → 27(0부터)
func col(ref string) int {
	n := 0
	for _, c := range ref {
		if c < 'A' || c > 'Z' {
			break
		}
		n = n*26 + int(c-'A'+1)
	}
	return n - 1
}

func main() {
	in := flag.String("in", "", "xlsx path")
	out := flag.String("out", "", "csv output path")
	dump := flag.Int("dump", 0, "print first N raw rows and exit")
	flag.Parse()
	if *in == "" {
		log.Fatal("-in required")
	}
	zr, err := zip.OpenReader(*in)
	if err != nil {
		log.Fatal(err)
	}
	defer zr.Close()
	var strs []string
	if b, err := readEntry(zr, "xl/sharedStrings.xml"); err == nil {
		var s sst
		if err := xml.Unmarshal(b, &s); err != nil {
			log.Fatal(err)
		}
		for _, si := range s.SI {
			t := si.T
			for _, r := range si.R {
				t += r.T
			}
			strs = append(strs, t)
		}
	}
	b, err := readEntry(zr, "xl/worksheets/sheet1.xml")
	if err != nil {
		log.Fatal(err)
	}
	var sh sheet
	if err := xml.Unmarshal(b, &sh); err != nil {
		log.Fatal(err)
	}
	var rows [][]string
	for _, r := range sh.Rows {
		var cells []string
		for _, c := range r.C {
			i := col(c.R)
			for len(cells) <= i {
				cells = append(cells, "")
			}
			v := c.V
			switch c.T {
			case "s":
				k, err := strconv.Atoi(v)
				if err != nil || k < 0 || k >= len(strs) {
					log.Fatalf("bad shared string ref %q", v)
				}
				v = strs[k]
			case "inlineStr":
				v = c.IS.T
			}
			cells[i] = strings.TrimSpace(v)
		}
		rows = append(rows, cells)
	}
	if *dump > 0 {
		for i := 0; i < *dump && i < len(rows); i++ {
			fmt.Printf("%d: %q\n", i, rows[i])
		}
		fmt.Printf("rows=%d\n", len(rows))
		return
	}
	if *out == "" {
		log.Fatal("-out required")
	}
	if err := convert(rows, *out); err != nil {
		log.Fatal(err)
	}
}

// convert는 헤더를 확인하고 행정구역 행을 검증해 CSV로 쓴다.
// 출력 열: code,l1,l2,l3,nx,ny,lat,lon (위경도는 소수 6자리)
func convert(rows [][]string, out string) error {
	want := []string{"구분", "행정구역코드", "1단계", "2단계", "3단계", "격자 X", "격자 Y"}
	if len(rows) < 2 || len(rows[0]) < 15 {
		return fmt.Errorf("unexpected sheet shape")
	}
	for i, w := range want {
		if rows[0][i] != w {
			return fmt.Errorf("header[%d]=%q, want %q", i, rows[0][i], w)
		}
	}
	if rows[0][13] != "경도(초/100)" || rows[0][14] != "위도(초/100)" {
		return fmt.Errorf("header lon/lat = %q %q", rows[0][13], rows[0][14])
	}
	var recs [][]string
	seenCode := map[string]bool{}
	grids := map[string]bool{}
	for i, r := range rows[1:] {
		for len(r) < 15 {
			r = append(r, "")
		}
		code := r[1]
		if len(code) != 10 || strings.Trim(code, "0123456789") != "" {
			return fmt.Errorf("row %d: bad code %q", i+2, code)
		}
		if seenCode[code] {
			return fmt.Errorf("row %d: duplicate code %s", i+2, code)
		}
		seenCode[code] = true
		nx, err1 := strconv.Atoi(r[5])
		ny, err2 := strconv.Atoi(r[6])
		lon, err3 := strconv.ParseFloat(r[13], 64)
		lat, err4 := strconv.ParseFloat(r[14], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			return fmt.Errorf("row %d: bad number %q", i+2, r)
		}
		noPos := lat == 0 && lon == 0 // 이어도: 대표 좌표가 0으로 들어 있음 → 위경도 없이 둔다
		if nx < 1 || nx > 149 || ny < 1 || ny > 253 || (!noPos && (lat < 32 || lat > 44 || lon < 123 || lon > 133)) {
			return fmt.Errorf("row %d: out of range %q", i+2, r)
		}
		for _, s := range r[2:5] {
			if strings.ContainsAny(s, ",\"\n\r") {
				return fmt.Errorf("row %d: unexpected char in name %q", i+2, s)
			}
		}
		if r[2] == "" {
			return fmt.Errorf("row %d: empty level 1", i+2)
		}
		grids[r[5]+","+r[6]] = true
		la, lo := strconv.FormatFloat(lat, 'f', 6, 64), strconv.FormatFloat(lon, 'f', 6, 64)
		if noPos {
			la, lo = "", ""
			fmt.Fprintf(os.Stderr, "row %d: %s %s has no position, kept without lat/lon\n", i+2, code, r[2])
		}
		recs = append(recs, []string{code, r[2], r[3], r[4], r[5], r[6], la, lo})
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.Write([]string{"code", "l1", "l2", "l3", "nx", "ny", "lat", "lon"})
	w.WriteAll(recs)
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: %d regions, %d grids\n", out, len(recs), len(grids))
	return nil
}
