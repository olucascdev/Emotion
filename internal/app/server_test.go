package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func authenticatedCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`))
	login.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, login)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d", recorder.Code)
	}
	return recorder.Result().Cookies()[0]
}

func TestPaymentAndExpenseFlow(t *testing.T) {
	s := testServer(t)
	cookie := authenticatedCookie(t, s)
	order := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"customer_name":"Ana","service_type":"Portrait","total_cents":10000,"installments":2}`))
	order.Header.Set("Content-Type", "application/json")
	order.AddCookie(cookie)
	orderRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(orderRecorder, order)
	if orderRecorder.Code != http.StatusCreated {
		t.Fatalf("order status = %d", orderRecorder.Code)
	}
	var created map[string]any
	_ = json.NewDecoder(orderRecorder.Body).Decode(&created)
	orderID := int64(created["id"].(float64))
	detail := httptest.NewRequest(http.MethodGet, "/api/orders/"+strconv.FormatInt(orderID, 10), nil)
	detail.AddCookie(cookie)
	detailRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d", detailRecorder.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(detailRecorder.Body).Decode(&body)
	installments := body["installments"].([]any)
	first := installments[0].(map[string]any)
	receivableID := int64(first["id"].(float64))
	payment := httptest.NewRequest(http.MethodPost, "/api/payments", strings.NewReader(`{"receivable_id":1,"amount_cents":5000,"method":"pix"}`))
	payment.Header.Set("Content-Type", "application/json")
	payment.AddCookie(cookie)
	paymentRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(paymentRecorder, payment)
	if paymentRecorder.Code != http.StatusCreated {
		t.Fatalf("payment status=%d body=%s", paymentRecorder.Code, paymentRecorder.Body.String())
	}
	_ = receivableID
	expense := httptest.NewRequest(http.MethodPost, "/api/expenses", strings.NewReader(`{"description":"Taxi","category":"transporte","amount_cents":3000}`))
	expense.Header.Set("Content-Type", "application/json")
	expense.AddCookie(cookie)
	expenseRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(expenseRecorder, expense)
	if expenseRecorder.Code != http.StatusCreated {
		t.Fatalf("expense status=%d", expenseRecorder.Code)
	}
	var createdExpense map[string]any
	_ = json.NewDecoder(expenseRecorder.Body).Decode(&createdExpense)
	expenseID := int64(createdExpense["id"].(float64))
	payExpense := httptest.NewRequest(http.MethodPost, "/api/expenses/"+strconv.FormatInt(expenseID, 10)+"/pay", strings.NewReader(`{"method":"cash"}`))
	payExpense.Header.Set("Content-Type", "application/json")
	payExpense.AddCookie(cookie)
	payRecorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(payRecorder, payExpense)
	if payRecorder.Code != http.StatusOK {
		t.Fatalf("pay expense status=%d", payRecorder.Code)
	}
	_ = receivableID
}

func TestAppointmentRejectsConflict(t *testing.T) {
	s := testServer(t)
	cookie := authenticatedCookie(t, s)
	create := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/appointments", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if code := create(`{"title":"Ensaio","starts_at":"2026-11-03T14:00:00-03:00","ends_at":"2026-11-03T15:00:00-03:00"}`); code != http.StatusCreated {
		t.Fatalf("first appointment status=%d", code)
	}
	if code := create(`{"title":"Conflito","starts_at":"2026-11-03T14:30:00-03:00","ends_at":"2026-11-03T15:30:00-03:00"}`); code != http.StatusConflict {
		t.Fatalf("conflict status=%d", code)
	}
}
