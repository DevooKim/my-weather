package kma_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"myweather/internal/kma"
	"myweather/internal/kma/kmatest"
	"myweather/internal/kst"
)

var base = time.Date(2026, 11, 9, 20, 0, 0, 0, kst.Loc)

func fetch(t *testing.T, srv *kmatest.Server, op kma.Op) kma.Result {
	t.Helper()
	c := kma.New(srv.URL, kmatest.Key, kma.Options{Timeout: 2 * time.Second, PerSecond: 1000})
	b := base
	if op == kma.OpUltra {
		b = b.Add(30 * time.Minute)
	}
	return c.Fetch(context.Background(), op, 61, 125, b)
}

func TestFetchOKAllOps(t *testing.T) {
	srv := kmatest.NewServer(nil)
	defer srv.Close()
	for _, op := range kma.Ops {
		r := fetch(t, srv, op)
		if !r.OK() || len(r.Items) == 0 || r.TotalCount != len(r.Items) {
			t.Fatalf("%s: %+v", op, r)
		}
	}
	reqs := srv.Requests()
	if reqs[0].Key != kmatest.Key {
		t.Fatalf("key must be appended verbatim, got %q", reqs[0].Key)
	}
	if reqs[2].Op != "getVilageFcst" || reqs[2].Base != 202611092000 || reqs[1].Base != 202611092030 {
		t.Fatalf("bad request %+v", reqs)
	}
}

func TestFetchNcstItemsUseObsTime(t *testing.T) {
	srv := kmatest.NewServer(nil)
	defer srv.Close()
	r := fetch(t, srv, kma.OpNcst)
	for _, it := range r.Items {
		if it.At != 202611092000 {
			t.Fatalf("ncst At=%d", it.At)
		}
	}
}

func TestFetchFailures(t *testing.T) {
	cases := []struct {
		name      string
		resp      kmatest.Responder
		code      string
		retryable bool
	}{
		{"nodata", func(kmatest.Req) (int, string) { return 200, kmatest.NoData }, "03", true},
		{"partial", func(r kmatest.Req) (int, string) {
			items := kmatest.Items(r, 0)
			return 200, kmatest.Envelope(items[:50], len(items))
		}, kma.CodePartial, true},
		{"zero total", func(kmatest.Req) (int, string) { return 200, kmatest.Envelope(nil, 0) }, kma.CodePartial, true},
		{"empty items string", func(kmatest.Req) (int, string) {
			return 200, `{"response":{"header":{"resultCode":"00","resultMsg":"OK"},"body":{"items":"","totalCount":0}}}`
		}, kma.CodePartial, true},
		{"500 app error", func(kmatest.Req) (int, string) { return 500, kmatest.AppError500 }, "HTTP500", true},
		{"403 auth", func(kmatest.Req) (int, string) { return 403, kmatest.AuthError403 }, kma.CodeAuth, false},
		{"200 svc auth", func(kmatest.Req) (int, string) { return 200, kmatest.AuthError403 }, kma.CodeAuth, false},
		{"200 xml quota", func(kmatest.Req) (int, string) {
			return 200, `<OpenAPI_ServiceResponse><cmmMsgHeader><returnReasonCode>22</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`
		}, "SVC:22", false},
		{"not json", func(kmatest.Req) (int, string) { return 200, "<html>oops" }, kma.CodeParse, true},
		{"other result code", func(kmatest.Req) (int, string) {
			return 200, `{"response":{"header":{"resultCode":"10","resultMsg":"INVALID_REQUEST_PARAMETER_ERROR"}}}`
		}, "10", false},
		{"400", func(kmatest.Req) (int, string) { return 400, "bad" }, "HTTP400", false},
		{"mismatch grid", func(r kmatest.Req) (int, string) {
			r.NX = 99
			return 200, kmatest.Body(r, 0)
		}, kma.CodeMismatch, true},
		{"mismatch base", func(r kmatest.Req) (int, string) {
			r.Base = 202611091700
			return 200, kmatest.Body(r, 0)
		}, kma.CodeMismatch, true},
		{"missing category", func(r kmatest.Req) (int, string) {
			var keep []map[string]any
			for _, it := range kmatest.Items(r, 0) {
				if it["category"] != "PCP" {
					keep = append(keep, it)
				}
			}
			return 200, kmatest.Envelope(keep, len(keep))
		}, kma.CodeMissing, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := kmatest.NewServer(c.resp)
			defer srv.Close()
			r := fetch(t, srv, kma.OpVilage)
			if r.Code != c.code || r.Retryable() != c.retryable || r.OK() || r.Items != nil {
				t.Fatalf("got code=%q retryable=%v items=%d err=%q", r.Code, r.Retryable(), len(r.Items), r.Err)
			}
			if r.Err == "" {
				t.Fatal("Err should be recorded on failure")
			}
		})
	}
}

func TestFetchStringNumbers(t *testing.T) {
	// nx/ny가 문자열, totalCount가 문자열이어도 받는다.
	srv := kmatest.NewServer(func(r kmatest.Req) (int, string) {
		b := kmatest.Body(r, 0)
		b = strings.ReplaceAll(b, `"nx":61`, `"nx":"61"`)
		b = strings.ReplaceAll(b, `"ny":131`, `"ny":"131"`)
		return 200, b
	})
	defer srv.Close()
	if r := fetch(t, srv, kma.OpNcst); !r.OK() {
		t.Fatalf("%+v", r)
	}
}

func TestKeyRedactedOnNetworkError(t *testing.T) {
	// 연결을 받자마자 끊는 서버: *url.Error 메시지에 URL(키 포함)이 들어간다.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	c := kma.New("http://"+ln.Addr().String()+"/", kmatest.Key, kma.Options{Timeout: time.Second, PerSecond: 1000})
	r := c.Fetch(context.Background(), kma.OpNcst, 61, 125, base)
	if !strings.HasPrefix(r.Code, "NET:") || !r.Retryable() {
		t.Fatalf("code=%q", r.Code)
	}
	if !strings.Contains(r.Err, "***") || strings.Contains(r.Err, "TEST%2BKEY") || strings.Contains(r.Err, "TEST+KEY") {
		t.Fatalf("key not redacted: %q", r.Err)
	}
}

func TestKeyRedactedOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	c := kma.New(srv.URL, kmatest.Key, kma.Options{Timeout: 50 * time.Millisecond, PerSecond: 1000})
	r := c.Fetch(context.Background(), kma.OpNcst, 61, 125, base)
	if r.Code != "NET:timeout" {
		t.Fatalf("code=%q err=%q", r.Code, r.Err)
	}
	if strings.Contains(r.Err, "TEST%2BKEY") || strings.Contains(r.Err, "TEST+KEY") {
		t.Fatalf("key not redacted: %q", r.Err)
	}
}

func TestKeyRedactedInBodyEcho(t *testing.T) {
	// 서버가 URL을 본문에 그대로 돌려줘도 Err에는 키가 남지 않는다.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(r.URL.RequestURI() + " decoded=" + r.URL.Query().Get("serviceKey")))
	}))
	defer srv.Close()
	c := kma.New(srv.URL, kmatest.Key, kma.Options{PerSecond: 1000})
	r := c.Fetch(context.Background(), kma.OpNcst, 61, 125, base)
	if strings.Contains(r.Err, "TEST%2BKEY") || strings.Contains(r.Err, "TEST+KEY") {
		t.Fatalf("key not redacted: %q", r.Err)
	}
}

func TestDecodedKeyIsEncoded(t *testing.T) {
	srv := kmatest.NewServer(nil)
	defer srv.Close()
	c := kma.New(srv.URL, "TEST+KEY==abc123", kma.Options{PerSecond: 1000})
	if r := c.Fetch(context.Background(), kma.OpNcst, 61, 125, base); !r.OK() {
		t.Fatalf("%+v", r)
	}
	if k := srv.Requests()[0].Key; k != "TEST%2BKEY%3D%3Dabc123" {
		t.Fatalf("key=%q", k)
	}
}

func TestUltraItemBaseLabelledHour(t *testing.T) {
	// 실측: base_time=2330 요청에 item baseTime "2300"이 온다. 같은 시각의 hh00은 받아들인다.
	srv := kmatest.NewServer(func(r kmatest.Req) (int, string) {
		r.Base -= 30
		return 200, kmatest.Body(r, 0)
	})
	defer srv.Close()
	if r := fetch(t, srv, kma.OpUltra); !r.OK() {
		t.Fatalf("%+v", r)
	}
	// 한 시간 전(2230) 표기는 여전히 MISMATCH
	srv.SetResponder(func(r kmatest.Req) (int, string) {
		r.Base -= 100
		return 200, kmatest.Body(r, 0)
	})
	if r := fetch(t, srv, kma.OpUltra); r.Code != kma.CodeMismatch {
		t.Fatalf("%+v", r)
	}
	// 단기예보에는 적용하지 않는다
	srv.SetResponder(func(r kmatest.Req) (int, string) {
		r.Base -= 30
		return 200, kmatest.Body(r, 0)
	})
	if r := fetch(t, srv, kma.OpVilage); r.Code != kma.CodeMismatch {
		t.Fatalf("%+v", r)
	}
}
