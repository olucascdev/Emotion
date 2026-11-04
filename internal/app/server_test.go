package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Config{Port: "8080", DatabasePath: t.TempDir() + "/emotion.db", JWTSecret: "0123456789012345678901234567890123456789", AdminEmail: "admin@example.com", AdminPassword: "correct horse battery staple"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoginAndDashboard(t *testing.T) {
	s := testServer(t)
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`))
	login.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, login)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	cookie := recorder.Result().Cookies()[0]
	dashboard := httptest.NewRequest(http.MethodGet, "/api/dashboard?month=2026-11", nil)
	dashboard.AddCookie(cookie)
	recorder = httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, dashboard)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", recorder.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["month"] != "2026-11" {
		t.Fatalf("unexpected month: %#v", body["month"])
	}
}

func TestProtectedRouteRequiresCookie(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, r)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestCreateOrderRejectsOverpayment(t *testing.T) {
	s := testServer(t)
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`))
	login.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, login)
	cookie := recorder.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"customer_name":"Maria","service_type":"Wedding","total_cents":10000,"initial_payment_cents":10001}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	recorder = httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, r)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateOrderAllocatesInitialPayment(t *testing.T) {
	s := testServer(t)
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`))
	login.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(loginRecorder, login)
	cookie := loginRecorder.Result().Cookies()[0]
	order := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"customer_name":"Maria","service_type":"Wedding","total_cents":120000,"installments":12,"initial_payment_cents":50000,"payment_method":"pix"}`))
	order.Header.Set("Content-Type", "application/json")
	order.AddCookie(cookie)
	orderRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(orderRecorder, order)
	if orderRecorder.Code != http.StatusCreated {
		t.Fatalf("order status = %d, body=%s", orderRecorder.Code, orderRecorder.Body.String())
	}
	dashboard := httptest.NewRequest(http.MethodGet, "/api/dashboard?month="+""+"", nil)
	dashboard.AddCookie(cookie)
	dashboardRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(dashboardRecorder, dashboard)
	if dashboardRecorder.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", dashboardRecorder.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(dashboardRecorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["received_cents"] != float64(50000) {
		t.Fatalf("received_cents = %v", body["received_cents"])
	}
	if body["receivable_cents"] != float64(70000) {
		t.Fatalf("receivable_cents = %v", body["receivable_cents"])
	}
}
