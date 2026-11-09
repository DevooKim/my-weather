// Package kmatest는 테스트용 KMA 고정 응답과 가짜 서버를 제공한다. 실제 API를 부르지 않는다.
package kmatest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"myweather/internal/kst"
)

// Key는 테스트용 가짜 serviceKey다('%'가 들어 있어 그대로 붙는 경로를 탄다).
const Key = "TEST%2BKEY%3D%3Dabc123"

// Req는 가짜 서버가 받은 요청이다.
type Req struct {
	Op     string
	NX, NY int
	Base   int64
	Key    string // URL에 들어온 serviceKey 원문
}

// Responder는 요청에 대한 응답(상태, 본문)을 정한다.
type Responder func(r Req) (int, string)

// Server는 요청을 기록하는 가짜 KMA 서버다.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	reqs    []Req
	respond Responder
}

// NewServer는 respond로 응답하는 서버를 띄운다. nil이면 OK 응답을 만든다.
func NewServer(respond Responder) *Server {
	s := &Server{respond: respond}
	if s.respond == nil {
		s.respond = OK
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		nx, _ := strconv.Atoi(q.Get("nx"))
		ny, _ := strconv.Atoi(q.Get("ny"))
		b, _ := kst.ParseBase(q.Get("base_date"), q.Get("base_time"))
		// RawQuery에서 serviceKey 원문을 꺼낸다(디코딩 전).
		raw := r.URL.RawQuery
		key := ""
		for _, kv := range splitAmp(raw) {
			if len(kv) > 11 && kv[:11] == "serviceKey=" {
				key = kv[11:]
			}
		}
		req := Req{Op: r.URL.Path[len(r.URL.Path)-len(lastSeg(r.URL.Path)):], NX: nx, NY: ny, Base: b, Key: key}
		s.mu.Lock()
		s.reqs = append(s.reqs, req)
		fn := s.respond
		s.mu.Unlock()
		status, body := fn(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return s
}

// SetResponder는 응답 방식을 바꾼다.
func (s *Server) SetResponder(fn Responder) {
	if fn == nil {
		fn = OK
	}
	s.mu.Lock()
	s.respond = fn
	s.mu.Unlock()
}

// Requests는 지금까지 받은 요청 사본이다.
func (s *Server) Requests() []Req {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Req(nil), s.reqs...)
}

// Reset은 요청 기록을 지운다.
func (s *Server) Reset() {
	s.mu.Lock()
	s.reqs = nil
	s.mu.Unlock()
}

func splitAmp(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func lastSeg(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// OK는 오퍼레이션에 맞는 정상 응답을 만든다.
func OK(r Req) (int, string) { return 200, Body(r, 0) }

// Body는 정상 응답 본문이다. variant를 바꾸면 TMP/T1H 값이 달라진다(내용 변경 시뮬레이션).
func Body(r Req, variant int) string {
	items := Items(r, variant)
	return Envelope(items, len(items))
}

// Items는 오퍼레이션별 고정 item 목록이다.
func Items(r Req, variant int) []map[string]any {
	base := kst.FromInt(r.Base)
	bd, bt := base.Format("20060102"), base.Format("1504")
	mk := func(cat string, at time.Time, v string) map[string]any {
		m := map[string]any{"baseDate": bd, "baseTime": bt, "category": cat, "nx": r.NX, "ny": r.NY}
		if r.Op == "getUltraSrtNcst" {
			m["obsrValue"] = v
		} else {
			m["fcstDate"] = at.Format("20060102")
			m["fcstTime"] = at.Format("1504")
			m["fcstValue"] = v
		}
		return m
	}
	var out []map[string]any
	switch r.Op {
	case "getUltraSrtNcst":
		for _, c := range []string{"PTY", "REH", "RN1", "T1H", "UUU", "VEC", "VVV", "WSD"} {
			v := "0"
			if c == "T1H" {
				v = fmt.Sprintf("%.1f", 7.9+float64(variant))
			}
			out = append(out, mk(c, base, v))
		}
	case "getUltraSrtFcst":
		start := base.Truncate(time.Hour).Add(time.Hour)
		for h := 0; h < 6; h++ {
			at := start.Add(time.Duration(h) * time.Hour)
			for _, c := range []string{"LGT", "PTY", "RN1", "SKY", "T1H", "REH", "UUU", "VVV", "VEC", "WSD"} {
				v := "0"
				switch c {
				case "T1H":
					v = strconv.Itoa(8 + h + variant)
				case "RN1":
					v = "강수없음"
				case "SKY":
					v = "1"
				}
				out = append(out, mk(c, at, v))
			}
		}
	default: // getVilageFcst
		start := base.Add(time.Hour)
		for h := 0; h < 12; h++ {
			at := start.Add(time.Duration(h) * time.Hour)
			for _, c := range []string{"TMP", "UUU", "VVV", "VEC", "WSD", "SKY", "PTY", "POP", "WAV", "PCP", "REH", "SNO"} {
				v := "0"
				switch c {
				case "TMP":
					v = strconv.Itoa(10 + h + variant)
				case "PCP":
					v = "강수없음"
				case "SNO":
					v = "적설없음"
				case "SKY":
					v = "3"
				case "POP":
					v = "20"
				}
				out = append(out, mk(c, at, v))
			}
		}
	}
	return out
}

// Envelope는 items와 totalCount로 응답 본문을 만든다(잘린 응답 시뮬레이션에 쓰려고 분리).
func Envelope(items []map[string]any, total int) string {
	m := map[string]any{"response": map[string]any{
		"header": map[string]any{"resultCode": "00", "resultMsg": "NORMAL_SERVICE"},
		"body": map[string]any{
			"dataType": "JSON", "items": map[string]any{"item": items},
			"pageNo": 1, "numOfRows": 1200, "totalCount": total,
		},
	}}
	b, _ := json.Marshal(m)
	return string(b)
}

// NoData는 NO_DATA 응답이다(body 키가 없음).
const NoData = `{"response":{"header":{"resultCode":"03","resultMsg":"NO_DATA"}}}`

// AppError500은 HTTP 500과 함께 오는 게이트웨이 오류 본문이다.
const AppError500 = `{"OpenAPI_ServiceResponse":{"cmmMsgHeader":{"errMsg":"APPLICATION_ERROR","returnAuthMsg":"어플리케이션 에러","returnReasonCode":"01"}}}`

// AuthError403은 키 오류 본문이다.
const AuthError403 = `{"OpenAPI_ServiceResponse":{"cmmMsgHeader":{"errMsg":"SERVICE ERROR","returnAuthMsg":"SERVICE_KEY_IS_NOT_REGISTERED_ERROR","returnReasonCode":"30"}}}`
