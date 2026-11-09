// Package kma는 data.go.kr 기상청 단기예보 조회서비스(VilageFcstInfoService_2.0) 클라이언트다.
//
// - serviceKey는 인코딩된 값을 그대로 URL에 붙인다(url.Values를 거치면 '%'가 두 번 인코딩된다).
// - 응답을 설계 §5.2 기준으로 검증하고, 실패 사유를 결과 코드 하나로 분류한다.
// - 오류 문자열에서 키 값을 가린다(*url.Error 메시지에 URL 전체가 들어가기 때문).
package kma

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"myweather/internal/kst"
)

// DefaultBaseURL은 실제 서비스 주소다. 테스트는 httptest 주소를 넣는다.
const DefaultBaseURL = "https://apis.data.go.kr/1360000/VilageFcstInfoService_2.0/"

// Op는 상세기능(오퍼레이션)이다.
type Op string

const (
	OpNcst   Op = "getUltraSrtNcst"
	OpUltra  Op = "getUltraSrtFcst"
	OpVilage Op = "getVilageFcst"
)

// Ops는 수집 대상 오퍼레이션 목록이다.
var Ops = []Op{OpNcst, OpUltra, OpVilage}

// Kind는 발표 주기다.
func (o Op) Kind() kst.Kind {
	switch o {
	case OpUltra:
		return kst.Ultra
	case OpVilage:
		return kst.Vilage
	default:
		return kst.Ncst
	}
}

// NumOfRows는 한 페이지로 다 받도록 넉넉히 잡은 값이다(설계 §5.2-4).
func (o Op) NumOfRows() int {
	switch o {
	case OpVilage:
		return 1200
	case OpUltra:
		return 100
	default:
		return 20
	}
}

// Short는 DB의 fcst_version.op 값('vilage' | 'ultra')이다. 실황은 'ncst'.
func (o Op) Short() string {
	switch o {
	case OpVilage:
		return "vilage"
	case OpUltra:
		return "ultra"
	default:
		return "ncst"
	}
}

// Required는 반드시 있어야 하는 카테고리다(설계 §5.2-6).
func (o Op) Required() []string {
	switch o {
	case OpVilage:
		return []string{"TMP", "SKY", "PTY", "POP", "PCP"}
	case OpUltra:
		return []string{"T1H", "PTY", "RN1", "SKY"}
	default:
		return []string{"T1H", "PTY", "RN1"}
	}
}

// Item은 응답 행 하나다. 실황이면 At은 관측 시각(=base), 예보면 fcstDate+fcstTime이다.
type Item struct {
	At       int64
	Category string
	Value    string
}

// 결과 코드 (kma_call.result_code)
const (
	CodeOK       = "00"
	CodeNoData   = "03"
	CodePartial  = "PARTIAL"
	CodeAuth     = "AUTH"
	CodeParse    = "PARSE"
	CodeMismatch = "MISMATCH" // base·nx·ny가 요청과 다름
	CodeMissing  = "MISSING"  // 필수 카테고리 없음
	CodeConfig   = "CONFIG"   // totalCount > numOfRows
)

// Result는 호출 한 번의 결과다. Code가 "00"일 때만 Items가 유효하다.
type Result struct {
	HTTPStatus int // 0 = 네트워크 오류
	Code       string
	Items      []Item
	NItems     int // 응답에 들어 있던 item 수 (검증 실패여도 기록)
	TotalCount int
	Duration   time.Duration
	Err        string // 실패 시 본문 앞 500자 또는 오류 문자열. 키는 '***'로 가림
}

// OK는 검증까지 통과했는지다.
func (r Result) OK() bool { return r.Code == CodeOK }

// Retryable은 같은 발표분을 다시 시도할 대상인지다(설계 §5.3).
// AUTH, 4xx, 그 밖의 결과코드(쿼터·파라미터 오류 등)는 재시도하지 않는다.
func (r Result) Retryable() bool {
	switch r.Code {
	case CodeNoData, CodePartial, CodeParse, CodeMismatch, CodeMissing, "SVC:01":
		return true
	}
	if strings.HasPrefix(r.Code, "NET:") {
		return true
	}
	if strings.HasPrefix(r.Code, "HTTP5") {
		return true
	}
	return false
}

// Client는 KMA 호출 클라이언트다. 동시 호출 수와 초당 호출 수를 제한한다.
type Client struct {
	baseURL string
	key     string // URL에 그대로 붙일 인코딩된 값
	redact  []string
	http    *http.Client

	sem      chan struct{}
	mu       sync.Mutex
	nextSlot time.Time
	interval time.Duration
}

// Options는 클라이언트 설정이다. 0이면 기본값.
type Options struct {
	Timeout     time.Duration // 기본 20초
	Concurrency int           // 기본 4
	PerSecond   int           // 기본 10
}

// New는 클라이언트를 만든다. key는 data.go.kr 'Encoding' 키(그대로 붙임)나 'Decoding' 키(여기서 인코딩) 모두 받는다.
func New(baseURL, key string, o Options) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.Concurrency == 0 {
		o.Concurrency = 4
	}
	if o.PerSecond == 0 {
		o.PerSecond = 10
	}
	enc := key
	if !strings.Contains(key, "%") {
		enc = url.QueryEscape(key)
	}
	red := []string{enc}
	if dec, err := url.QueryUnescape(enc); err == nil && dec != enc {
		red = append(red, dec)
	}
	if key != enc {
		red = append(red, key)
	}
	return &Client{
		baseURL:  baseURL,
		key:      enc,
		redact:   red,
		http:     &http.Client{Timeout: o.Timeout},
		sem:      make(chan struct{}, o.Concurrency),
		interval: time.Second / time.Duration(o.PerSecond),
	}
}

// Redact는 문자열에서 키 값을 가린다.
func (c *Client) Redact(s string) string {
	for _, k := range c.redact {
		if k != "" {
			s = strings.ReplaceAll(s, k, "***")
		}
	}
	return s
}

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	slot := c.nextSlot
	if slot.Before(now) {
		slot = now
	}
	c.nextSlot = slot.Add(c.interval)
	c.mu.Unlock()
	if d := time.Until(slot); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *Client) buildURL(op Op, nx, ny int, base time.Time) string {
	base = base.In(kst.Loc)
	q := url.Values{}
	q.Set("pageNo", "1")
	q.Set("numOfRows", strconv.Itoa(op.NumOfRows()))
	q.Set("dataType", "JSON")
	q.Set("base_date", base.Format("20060102"))
	q.Set("base_time", base.Format("1504"))
	q.Set("nx", strconv.Itoa(nx))
	q.Set("ny", strconv.Itoa(ny))
	// 키는 url.Values 밖에서 그대로 붙인다.
	return c.baseURL + string(op) + "?serviceKey=" + c.key + "&" + q.Encode()
}

// Fetch는 (op, nx, ny, base) 하나를 호출하고 검증한다. 오류는 Result.Code로만 알린다.
func (c *Client) Fetch(ctx context.Context, op Op, nx, ny int, base time.Time) Result {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return Result{Code: "NET:canceled", Err: ctx.Err().Error()}
	}
	defer func() { <-c.sem }()
	if err := c.wait(ctx); err != nil {
		return Result{Code: "NET:canceled", Err: err.Error()}
	}

	t0 := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.buildURL(op, nx, ny, base), nil)
	if err != nil {
		return Result{Code: CodeParse, Err: c.Redact(err.Error()), Duration: time.Since(t0)}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{Code: netCode(err), Err: trunc(c.Redact(err.Error())), Duration: time.Since(t0)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	dur := time.Since(t0)
	if err != nil {
		return Result{HTTPStatus: resp.StatusCode, Code: netCode(err), Err: trunc(c.Redact(err.Error())), Duration: dur}
	}
	r := c.parse(op, nx, ny, base, resp.StatusCode, body)
	r.Duration = dur
	if !r.OK() && r.Err == "" {
		r.Err = trunc(c.Redact(string(body)))
	}
	return r
}

func trunc(s string) string {
	if len(s) <= 500 {
		return s
	}
	// UTF-8 경계에서 자른다.
	i := 500
	for i > 0 && (s[i]&0xC0) == 0x80 {
		i--
	}
	return s[:i]
}

func netCode(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "NET:timeout"
	case errors.Is(err, context.Canceled):
		return "NET:canceled"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "NET:eof"
	}
	return "NET:error"
}

// flexString은 JSON 문자열과 숫자를 모두 받는다.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = flexString(b)
	return nil
}

type rawItem struct {
	BaseDate  flexString `json:"baseDate"`
	BaseTime  flexString `json:"baseTime"`
	Category  flexString `json:"category"`
	FcstDate  flexString `json:"fcstDate"`
	FcstTime  flexString `json:"fcstTime"`
	FcstValue flexString `json:"fcstValue"`
	ObsrValue flexString `json:"obsrValue"`
	NX        flexString `json:"nx"`
	NY        flexString `json:"ny"`
}

// items는 {"item":[...]} 또는 빈 문자열 ""로 올 수 있다.
type itemsField struct {
	Item []rawItem `json:"item"`
}

func (it *itemsField) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] == '"' || string(b) == "null" {
		return nil
	}
	type alias itemsField
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*it = itemsField(a)
	return nil
}

type envelope struct {
	Response *struct {
		Header struct {
			ResultCode flexString `json:"resultCode"`
			ResultMsg  flexString `json:"resultMsg"`
		} `json:"header"`
		Body *struct {
			Items      itemsField `json:"items"`
			NumOfRows  flexString `json:"numOfRows"`
			TotalCount flexString `json:"totalCount"`
		} `json:"body"`
	} `json:"response"`
	OpenAPI *struct {
		Header struct {
			ReasonCode flexString `json:"returnReasonCode"`
		} `json:"cmmMsgHeader"`
	} `json:"OpenAPI_ServiceResponse"`
}

var reasonRe = regexp.MustCompile(`<returnReasonCode>\s*(\d+)\s*</returnReasonCode>`)

// parse는 HTTP 상태와 본문으로 결과를 만든다. 상태 코드를 먼저 본다.
func (c *Client) parse(op Op, nx, ny int, base time.Time, status int, body []byte) Result {
	r := Result{HTTPStatus: status}
	switch {
	case status == http.StatusForbidden || status == http.StatusUnauthorized:
		r.Code = CodeAuth
		return r
	case status >= 500:
		r.Code = "HTTP" + strconv.Itoa(status)
		return r
	case status != http.StatusOK:
		r.Code = "HTTP" + strconv.Itoa(status)
		return r
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		// data.go.kr는 게이트웨이 오류를 JSON 요청에도 XML로 줄 때가 있다.
		if m := reasonRe.FindSubmatch(body); m != nil {
			r.Code = svcCode(string(m[1]))
			return r
		}
		r.Code = CodeParse
		return r
	}
	if env.Response == nil {
		if env.OpenAPI != nil {
			r.Code = svcCode(string(env.OpenAPI.Header.ReasonCode))
			return r
		}
		r.Code = CodeParse
		return r
	}
	code := strings.TrimSpace(string(env.Response.Header.ResultCode))
	if code != CodeOK {
		if code == "" {
			code = CodeParse
		}
		r.Code = code // "03" 등 결과코드 그대로
		return r
	}
	b := env.Response.Body
	if b == nil {
		r.Code = CodePartial
		r.Err = "resultCode 00 without body"
		return r
	}
	raw := b.Items.Item
	r.NItems = len(raw)
	total, err := strconv.Atoi(strings.TrimSpace(string(b.TotalCount)))
	if err != nil {
		r.Code = CodeParse
		r.Err = "bad totalCount"
		return r
	}
	r.TotalCount = total
	if total > op.NumOfRows() {
		r.Code = CodeConfig
		r.Err = fmt.Sprintf("totalCount %d > numOfRows %d", total, op.NumOfRows())
		return r
	}
	if total <= 0 || len(raw) != total {
		r.Code = CodePartial
		r.Err = fmt.Sprintf("items %d / totalCount %d", len(raw), total)
		return r
	}

	wantBase := kst.ToInt(base)
	items := make([]Item, 0, len(raw))
	seen := map[string]bool{}
	for _, it := range raw {
		b, err := kst.ParseBase(string(it.BaseDate), string(it.BaseTime))
		inx, err1 := strconv.Atoi(strings.TrimSpace(string(it.NX)))
		iny, err2 := strconv.Atoi(strings.TrimSpace(string(it.NY)))
		if err != nil || err1 != nil || err2 != nil {
			r.Code, r.Err = CodeParse, "bad item base/nx/ny"
			return r
		}
		// 초단기예보는 hh30으로 요청해도 item의 baseTime이 같은 시각의 hh00으로 온다
		// (2026-11-09 23:58 측정: 2330·2300 요청 모두 baseTime 2300, 내용 동일). 저장은 요청한 hh30으로 한다.
		baseOK := b == wantBase || (op == OpUltra && wantBase%100 == 30 && b == wantBase-30)
		if !baseOK || inx != nx || iny != ny {
			r.Code = CodeMismatch
			r.Err = fmt.Sprintf("item base=%d nx=%d ny=%d, want %d %d %d", b, inx, iny, wantBase, nx, ny)
			return r
		}
		cat := strings.TrimSpace(string(it.Category))
		if cat == "" {
			r.Code, r.Err = CodeParse, "empty category"
			return r
		}
		var at int64
		var val string
		if op == OpNcst {
			at, val = b, string(it.ObsrValue)
		} else {
			at, err = kst.ParseBase(string(it.FcstDate), string(it.FcstTime))
			if err != nil {
				r.Code, r.Err = CodeParse, "bad fcstDate/fcstTime"
				return r
			}
			val = string(it.FcstValue)
		}
		seen[cat] = true
		items = append(items, Item{At: at, Category: cat, Value: val})
	}
	for _, req := range op.Required() {
		if !seen[req] {
			r.Code, r.Err = CodeMissing, "missing category "+req
			return r
		}
	}
	r.Code = CodeOK
	r.Items = items
	return r
}

// svcCode: OpenAPI_ServiceResponse의 returnReasonCode. 키 관련(20·30·31·32)은 AUTH로 본다.
func svcCode(reason string) string {
	switch reason {
	case "20", "30", "31", "32":
		return CodeAuth
	case "":
		return CodeParse
	}
	return "SVC:" + reason
}
