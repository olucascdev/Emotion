package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

const (
	accessCookie = "emotion_access"
	roleAdmin    = "admin"
	roleManager  = "manager"
)

type Config struct {
	Port          string
	DatabasePath  string
	JWTSecret     string
	CookieSecure  bool
	AllowedOrigin string
	AdminEmail    string
	AdminPassword string
}

type Server struct {
	Port          string
	db            *sql.DB
	secret        []byte
	cookieSecure  bool
	allowedOrigin string
	loginMu       sync.Mutex
	loginFailures map[string]loginWindow
}

type loginWindow struct {
	count   int
	resetAt time.Time
}

type contextKey string

const userKey contextKey = "user"

type user struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type claims struct {
	Email string `json:"email"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

func NewServer(cfg Config) (*Server, error) {
	if len(cfg.JWTSecret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Server{Port: cfg.Port, db: db, secret: []byte(cfg.JWTSecret), cookieSecure: cfg.CookieSecure, allowedOrigin: cfg.AllowedOrigin, loginFailures: make(map[string]loginWindow)}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.ensureAdmin(cfg.AdminEmail, cfg.AdminPassword); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) migrate() error {
	_, err := s.db.Exec(`PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE, name TEXT NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','manager')), active INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS service_orders (id INTEGER PRIMARY KEY AUTOINCREMENT, number TEXT NOT NULL UNIQUE, customer_name TEXT NOT NULL, customer_phone TEXT, service_type TEXT NOT NULL, description TEXT, total_cents INTEGER NOT NULL CHECK(total_cents >= 0), status TEXT NOT NULL DEFAULT 'open', due_date TEXT, service_date TEXT, payment_plan_method TEXT, created_at TEXT NOT NULL, created_by INTEGER NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS receivables (id INTEGER PRIMARY KEY AUTOINCREMENT, order_id INTEGER NOT NULL REFERENCES service_orders(id), installment_number INTEGER NOT NULL, total_installments INTEGER NOT NULL, due_date TEXT NOT NULL, amount_cents INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'pending');
CREATE TABLE IF NOT EXISTS payments (id INTEGER PRIMARY KEY AUTOINCREMENT, order_id INTEGER REFERENCES service_orders(id), receivable_id INTEGER REFERENCES receivables(id), amount_cents INTEGER NOT NULL CHECK(amount_cents > 0), paid_at TEXT NOT NULL, method TEXT NOT NULL, note TEXT);
CREATE TABLE IF NOT EXISTS expenses (id INTEGER PRIMARY KEY AUTOINCREMENT, description TEXT NOT NULL, category TEXT NOT NULL, amount_cents INTEGER NOT NULL CHECK(amount_cents > 0), due_date TEXT, paid_at TEXT, method TEXT, status TEXT NOT NULL DEFAULT 'pending', created_at TEXT NOT NULL, created_by INTEGER NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS appointments (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, starts_at TEXT NOT NULL, ends_at TEXT NOT NULL, location TEXT, notes TEXT, order_id INTEGER REFERENCES service_orders(id), status TEXT NOT NULL DEFAULT 'scheduled', created_at TEXT NOT NULL, created_by INTEGER NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER REFERENCES users(id), action TEXT NOT NULL, entity TEXT NOT NULL, entity_id INTEGER, details TEXT, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_payments_paid_at ON payments(paid_at);
CREATE INDEX IF NOT EXISTS idx_expenses_status ON expenses(status);
CREATE INDEX IF NOT EXISTS idx_appointments_starts_at ON appointments(starts_at);`)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec("ALTER TABLE service_orders ADD COLUMN service_date TEXT"); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	if _, err = s.db.Exec("ALTER TABLE service_orders ADD COLUMN payment_plan_method TEXT"); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	return nil
}

func (s *Server) ensureAdmin(email, password string) error {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if email == "" || password == "" || password == "change-me-now" {
		return errors.New("set a non-default ADMIN_PASSWORD before first startup")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO users(email,name,password_hash,role,created_at) VALUES(?,?,?,?,?)", strings.ToLower(email), "Administrator", hash, roleAdmin, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.Handle("GET /api/auth/me", s.auth(http.HandlerFunc(s.me)))
	mux.Handle("GET /api/dashboard", s.auth(http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /api/orders", s.auth(http.HandlerFunc(s.listOrders)))
	mux.Handle("GET /api/orders/{id}", s.auth(http.HandlerFunc(s.getOrder)))
	mux.Handle("PUT /api/orders/{id}", s.auth(http.HandlerFunc(s.updateOrder)))
	mux.Handle("POST /api/orders", s.auth(http.HandlerFunc(s.createOrder)))
	mux.Handle("POST /api/payments", s.auth(http.HandlerFunc(s.createPayment)))
	mux.Handle("GET /api/expenses", s.auth(http.HandlerFunc(s.listExpenses)))
	mux.Handle("POST /api/expenses", s.auth(http.HandlerFunc(s.createExpense)))
	mux.Handle("POST /api/expenses/{id}/pay", s.auth(http.HandlerFunc(s.payExpense)))
	mux.Handle("GET /api/export/cashbook.csv", s.auth(http.HandlerFunc(s.exportCashbook)))
	mux.Handle("GET /api/appointments", s.auth(http.HandlerFunc(s.listAppointments)))
	mux.Handle("POST /api/appointments", s.auth(http.HandlerFunc(s.createAppointment)))
	mux.Handle("POST /api/appointments/{id}/cancel", s.auth(http.HandlerFunc(s.cancelAppointment)))
	mux.Handle("GET /api/users", s.auth(s.admin(http.HandlerFunc(s.listUsers))))
	mux.Handle("POST /api/users", s.auth(s.admin(http.HandlerFunc(s.createUser))))
	mux.Handle("GET /api/audit", s.auth(s.admin(http.HandlerFunc(s.listAudit))))
	return s.withHeaders(s.withRequestLog(s.withCORS(s.withStatic(mux))))
}

func (s *Server) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "null" && (s.allowedOrigin == "" || origin == s.allowedOrigin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("request method=%s path=%s remote=%s duration_ms=%d", r.Method, r.URL.Path, clientIP(r), time.Since(started).Milliseconds())
	})
}

func (s *Server) withStatic(api http.Handler) http.Handler {
	webRoot := http.Dir("web/dist")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(os.DirFS("web/dist"), strings.TrimPrefix(r.URL.Path, "/")); err == nil {
			http.FileServer(webRoot).ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(os.DirFS("web/dist"), "index.html"); err == nil {
			http.ServeFile(w, r, "web/dist/index.html")
			return
		}
		http.NotFound(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "emotion"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	client := clientIP(r)
	if !s.allowLogin(client) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var input struct{ Email, Password string }
	if !readJSON(w, r, &input) || input.Email == "" || input.Password == "" {
		return
	}
	var u user
	var hash string
	err := s.db.QueryRow("SELECT id,email,name,role,password_hash FROM users WHERE email = ? AND active = 1", strings.ToLower(strings.TrimSpace(input.Email))).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &hash)
	if err != nil || !verifyPassword(input.Password, hash) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.resetLogin(client)
	token, err := s.newToken(u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 900})
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, r.Context().Value(userKey))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(accessCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var c claims
		token, err := jwt.ParseWithClaims(cookie.Value, &c, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing algorithm")
			}
			return s.secret, nil
		})
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var u user
		if err := s.db.QueryRow("SELECT id,email,name,role FROM users WHERE id = ? AND active = 1", c.Subject).Scan(&u.ID, &u.Email, &u.Name, &u.Role); err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := r.Context().Value(userKey).(user); !ok || u.Role != roleAdmin {
			writeError(w, http.StatusForbidden, "administrator access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) newToken(u user) (string, error) {
	now := time.Now()
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{Email: u.Email, Role: u.Role, RegisteredClaims: jwt.RegisteredClaims{Issuer: "emotion", Subject: strconv.FormatInt(u.ID, 10), Audience: []string{"emotion-web"}, ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)), IssuedAt: jwt.NewNumericDate(now)}})
	return t.SignedString(s.secret)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if len(month) != 7 {
		month = time.Now().In(time.FixedZone("BRT", -3*60*60)).Format("2006-01")
	}
	var received, expenses, receivable, payable int64
	s.db.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM payments WHERE substr(paid_at,1,7)=?", month).Scan(&received)
	s.db.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM expenses WHERE status='paid' AND substr(paid_at,1,7)=?", month).Scan(&expenses)
	s.db.QueryRow("SELECT COALESCE(SUM(r.amount_cents - COALESCE((SELECT SUM(p.amount_cents) FROM payments p WHERE p.receivable_id=r.id),0)),0) FROM receivables r WHERE r.status IN ('pending','partial')").Scan(&receivable)
	s.db.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM expenses WHERE status='pending'").Scan(&payable)
	writeJSON(w, http.StatusOK, map[string]any{"month": month, "received_cents": received, "expenses_cents": expenses, "result_cents": received - expenses, "receivable_cents": receivable, "payable_cents": payable})
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id,number,customer_name,service_type,total_cents,status,COALESCE(due_date,''),COALESCE(service_date,''),created_at FROM service_orders ORDER BY id DESC LIMIT 100")
	if err != nil {
		writeError(w, 500, "could not list orders")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var number, name, typ, status, due, serviceDate, created string
		var total int64
		if rows.Scan(&id, &number, &name, &typ, &total, &status, &due, &serviceDate, &created) == nil {
			items = append(items, map[string]any{"id": id, "number": number, "customer_name": name, "service_type": typ, "total_cents": total, "status": status, "due_date": due, "service_date": serviceDate, "created_at": created})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}
	var order map[string]any = make(map[string]any)
	var number, customer, phone, serviceType, description, status, dueDate, serviceDate, paymentPlanMethod, createdAt string
	var total int64
	if err := s.db.QueryRow("SELECT number,customer_name,customer_phone,service_type,description,total_cents,status,COALESCE(due_date,''),COALESCE(service_date,''),COALESCE(payment_plan_method,''),created_at FROM service_orders WHERE id=?", id).Scan(&number, &customer, &phone, &serviceType, &description, &total, &status, &dueDate, &serviceDate, &paymentPlanMethod, &createdAt); err != nil {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	order["id"], order["number"], order["customer_name"], order["customer_phone"], order["service_type"] = id, number, customer, phone, serviceType
	order["description"], order["total_cents"], order["status"], order["due_date"], order["service_date"], order["payment_plan_method"], order["created_at"] = description, total, status, dueDate, serviceDate, paymentPlanMethod, createdAt
	rows, err := s.db.Query("SELECT r.id,r.installment_number,r.total_installments,r.due_date,r.amount_cents,r.status,COALESCE(SUM(p.amount_cents),0) FROM receivables r LEFT JOIN payments p ON p.receivable_id=r.id WHERE r.order_id=? GROUP BY r.id ORDER BY r.installment_number", id)
	if err != nil {
		writeError(w, 500, "could not load installments")
		return
	}
	defer rows.Close()
	installments := []map[string]any{}
	for rows.Next() {
		var rid int64
		var number, totalInstallments int
		var due, rstatus string
		var amount, paid int64
		if rows.Scan(&rid, &number, &totalInstallments, &due, &amount, &rstatus, &paid) == nil {
			installments = append(installments, map[string]any{"id": rid, "number": number, "total_installments": totalInstallments, "due_date": due, "amount_cents": amount, "paid_cents": paid, "balance_cents": amount - paid, "status": rstatus})
		}
	}
	order["installments"] = installments
	writeJSON(w, 200, order)
}

func (s *Server) updateOrder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, 400, "invalid order id")
		return
	}
	var input struct {
		CustomerName  string `json:"customer_name"`
		CustomerPhone string `json:"customer_phone"`
		Description   string `json:"description"`
		DueDate       string `json:"due_date"`
		Status        string `json:"status"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	allowed := map[string]bool{"open": true, "in_progress": true, "completed": true, "cancelled": true}
	if input.Status != "" && !allowed[input.Status] {
		writeError(w, 400, "invalid order status")
		return
	}
	result, err := s.db.Exec("UPDATE service_orders SET customer_name=COALESCE(NULLIF(?,''),customer_name),customer_phone=?,description=?,due_date=?,status=COALESCE(NULLIF(?,''),status) WHERE id=?", input.CustomerName, input.CustomerPhone, input.Description, input.DueDate, input.Status, id)
	if err != nil {
		writeError(w, 500, "could not update order")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, 404, "order not found")
		return
	}
	s.audit(r.Context().Value(userKey).(user), "update", "service_order", id, map[string]any{"status": input.Status})
	writeJSON(w, 200, map[string]any{"id": id, "status": input.Status})
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CustomerName        string `json:"customer_name"`
		CustomerPhone       string `json:"customer_phone"`
		ServiceType         string `json:"service_type"`
		Description         string `json:"description"`
		DueDate             string `json:"due_date"`
		ServiceDate         string `json:"service_date"`
		TotalCents          int64  `json:"total_cents"`
		Installments        int    `json:"installments"`
		FirstDueDate        string `json:"first_due_date"`
		PaymentMethod       string `json:"payment_method"`
		InstallmentMethod   string `json:"installment_method"`
		InitialPaymentCents int64  `json:"initial_payment_cents"`
	}
	if !readJSON(w, r, &input) || strings.TrimSpace(input.CustomerName) == "" || strings.TrimSpace(input.ServiceType) == "" || input.TotalCents <= 0 {
		if input.CustomerName == "" || input.ServiceType == "" || input.TotalCents <= 0 {
			writeError(w, http.StatusBadRequest, "customer, service type and positive total are required")
		}
		return
	}
	if input.Installments < 1 {
		input.Installments = 1
	}
	if input.Installments > 120 {
		writeError(w, 400, "installments must be between 1 and 120")
		return
	}
	if input.InitialPaymentCents < 0 || input.InitialPaymentCents > input.TotalCents {
		writeError(w, 400, "initial payment exceeds total")
		return
	}
	u := r.Context().Value(userKey).(user)
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		writeError(w, 500, "could not create order")
		return
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRow("INSERT INTO service_orders(number,customer_name,customer_phone,service_type,description,total_cents,status,due_date,service_date,payment_plan_method,created_at,created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id", fmt.Sprintf("OS-%s-%d", now.Format("20060102"), now.UnixNano()%100000), input.CustomerName, input.CustomerPhone, input.ServiceType, input.Description, input.TotalCents, "open", input.DueDate, input.ServiceDate, input.InstallmentMethod, now.Format(time.RFC3339), u.ID).Scan(&id); err != nil {
		writeError(w, 500, "could not create order")
		return
	}
	remainingTotal := input.TotalCents - input.InitialPaymentCents
	base := remainingTotal / int64(input.Installments)
	remainder := remainingTotal % int64(input.Installments)
	due, _ := time.Parse("2006-01-02", input.FirstDueDate)
	if input.FirstDueDate == "" {
		due = now
	}
	if input.InitialPaymentCents > 0 {
		var entryID int64
		if err = tx.QueryRow("INSERT INTO receivables(order_id,installment_number,total_installments,due_date,amount_cents,status) VALUES(?,?,?,?,?,?) RETURNING id", id, 0, input.Installments, now.Format("2006-01-02"), input.InitialPaymentCents, "paid").Scan(&entryID); err != nil {
			writeError(w, 500, "could not create initial payment")
			return
		}
		if _, err = tx.Exec("INSERT INTO payments(order_id,receivable_id,amount_cents,paid_at,method) VALUES(?,?,?,?,?)", id, entryID, input.InitialPaymentCents, now.Format(time.RFC3339), input.PaymentMethod); err != nil {
			writeError(w, 500, "could not create initial payment")
			return
		}
	}
	for i := 1; i <= input.Installments; i++ {
		amount := base
		if int64(i) <= remainder {
			amount++
		}
		dueDate := due.AddDate(0, i-1, 0).Format("2006-01-02")
		if _, err = tx.Exec("INSERT INTO receivables(order_id,installment_number,total_installments,due_date,amount_cents) VALUES(?,?,?,?,?)", id, i, input.Installments, dueDate, amount); err != nil {
			writeError(w, 500, "could not create receivables")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "could not save order")
		return
	}
	s.audit(r.Context().Value(userKey).(user), "create", "service_order", id, map[string]any{"total_cents": input.TotalCents, "installments": input.Installments})
	writeJSON(w, 201, map[string]any{"id": id, "number": fmt.Sprintf("OS-%s-%d", now.Format("20060102"), now.UnixNano()%100000)})
}

func (s *Server) createPayment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OrderID      int64  `json:"order_id"`
		ReceivableID int64  `json:"receivable_id"`
		AmountCents  int64  `json:"amount_cents"`
		Method       string `json:"method"`
		PaidAt       string `json:"paid_at"`
		Note         string `json:"note"`
	}
	if !readJSON(w, r, &input) || input.AmountCents <= 0 || (input.OrderID == 0 && input.ReceivableID == 0) {
		writeError(w, http.StatusBadRequest, "order, installment and positive amount are required")
		return
	}
	if input.Method == "" {
		input.Method = "pix"
	}
	if input.PaidAt == "" {
		input.PaidAt = time.Now().UTC().Format(time.RFC3339)
	}
	tx, err := s.db.Begin()
	if err != nil {
		writeError(w, 500, "could not start payment")
		return
	}
	defer tx.Rollback()
	if input.ReceivableID == 0 {
		if err = tx.QueryRow("SELECT id FROM receivables WHERE order_id=? AND status IN ('pending','partial') ORDER BY installment_number LIMIT 1", input.OrderID).Scan(&input.ReceivableID); err != nil {
			writeError(w, 404, "no pending installment found")
			return
		}
	}
	var orderID, amount int64
	var status string
	if err = tx.QueryRow("SELECT order_id,amount_cents,status FROM receivables WHERE id=?", input.ReceivableID).Scan(&orderID, &amount, &status); err != nil {
		writeError(w, 404, "installment not found")
		return
	}
	if input.OrderID != 0 && input.OrderID != orderID {
		writeError(w, 400, "installment does not belong to order")
		return
	}
	var paid int64
	if err = tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM payments WHERE receivable_id=?", input.ReceivableID).Scan(&paid); err != nil {
		writeError(w, 500, "could not read installment")
		return
	}
	if input.AmountCents > amount-paid {
		writeError(w, 400, "payment exceeds installment balance")
		return
	}
	if _, err = tx.Exec("INSERT INTO payments(order_id,receivable_id,amount_cents,paid_at,method,note) VALUES(?,?,?,?,?,?)", orderID, input.ReceivableID, input.AmountCents, input.PaidAt, input.Method, input.Note); err != nil {
		writeError(w, 500, "could not save payment")
		return
	}
	newPaid := paid + input.AmountCents
	newStatus := "partial"
	if newPaid == amount {
		newStatus = "paid"
	}
	if _, err = tx.Exec("UPDATE receivables SET status=? WHERE id=?", newStatus, input.ReceivableID); err != nil {
		writeError(w, 500, "could not update installment")
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "could not save payment")
		return
	}
	u := r.Context().Value(userKey).(user)
	s.audit(u, "create", "payment", input.ReceivableID, map[string]any{"order_id": orderID, "amount_cents": input.AmountCents, "method": input.Method})
	writeJSON(w, 201, map[string]any{"order_id": orderID, "receivable_id": input.ReceivableID, "status": newStatus})
}

func (s *Server) listExpenses(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id,description,category,amount_cents,due_date,paid_at,status FROM expenses ORDER BY COALESCE(due_date,created_at) DESC LIMIT 100")
	if err != nil {
		writeError(w, 500, "could not list expenses")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, amount int64
		var d, c, due, paid, status string
		if rows.Scan(&id, &d, &c, &amount, &due, &paid, &status) == nil {
			items = append(items, map[string]any{"id": id, "description": d, "category": c, "amount_cents": amount, "due_date": due, "paid_at": paid, "status": status})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) createExpense(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Description string `json:"description"`
		Category    string `json:"category"`
		DueDate     string `json:"due_date"`
		PaidAt      string `json:"paid_at"`
		Method      string `json:"method"`
		AmountCents int64  `json:"amount_cents"`
	}
	if !readJSON(w, r, &input) || strings.TrimSpace(input.Description) == "" || input.AmountCents <= 0 {
		if input.Description == "" || input.AmountCents <= 0 {
			writeError(w, http.StatusBadRequest, "description and positive amount are required")
		}
		return
	}
	status := "pending"
	if input.PaidAt != "" {
		status = "paid"
	}
	u := r.Context().Value(userKey).(user)
	res, err := s.db.Exec("INSERT INTO expenses(description,category,amount_cents,due_date,paid_at,method,status,created_at,created_by) VALUES(?,?,?,?,?,?,?,?,?)", input.Description, input.Category, input.AmountCents, input.DueDate, input.PaidAt, input.Method, status, time.Now().UTC().Format(time.RFC3339), u.ID)
	if err != nil {
		writeError(w, 500, "could not create expense")
		return
	}
	id, _ := res.LastInsertId()
	s.audit(r.Context().Value(userKey).(user), "create", "expense", id, map[string]any{"amount_cents": input.AmountCents, "status": status})
	writeJSON(w, 201, map[string]any{"id": id, "status": status})
}

func (s *Server) payExpense(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, 400, "invalid expense id")
		return
	}
	var input struct {
		Method string `json:"method"`
		PaidAt string `json:"paid_at"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.Method == "" {
		input.Method = "pix"
	}
	if input.PaidAt == "" {
		input.PaidAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := s.db.Exec("UPDATE expenses SET status='paid',paid_at=?,method=? WHERE id=? AND status!='paid'", input.PaidAt, input.Method, id)
	if err != nil {
		writeError(w, 500, "could not pay expense")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, 404, "pending expense not found")
		return
	}
	s.audit(r.Context().Value(userKey).(user), "pay", "expense", id, map[string]any{"method": input.Method})
	writeJSON(w, 200, map[string]any{"id": id, "status": "paid"})
}

func (s *Server) exportCashbook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=emotion-cashbook.csv")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"date", "type", "description", "method", "amount_cents"})
	rows, err := s.db.Query("SELECT paid_at,'income',COALESCE(o.number,'Payment'),p.method,p.amount_cents FROM payments p LEFT JOIN service_orders o ON o.id=p.order_id UNION ALL SELECT paid_at,'expense',description,COALESCE(method,''),-amount_cents FROM expenses WHERE status='paid' ORDER BY date DESC")
	if err != nil {
		writeError(w, 500, "could not export cashbook")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var date, typ, description, method string
		var amount int64
		if rows.Scan(&date, &typ, &description, &method, &amount) == nil {
			_ = writer.Write([]string{date, typ, description, method, strconv.FormatInt(amount, 10)})
		}
	}
	writer.Flush()
}

func (s *Server) listAppointments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id,title,starts_at,ends_at,location,notes,COALESCE(order_id,0),status FROM appointments WHERE starts_at >= ? ORDER BY starts_at LIMIT 100", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		writeError(w, 500, "could not list appointments")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var title, start, end, location, notes, status string
		var orderID int64
		if rows.Scan(&id, &title, &start, &end, &location, &notes, &orderID, &status) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "starts_at": start, "ends_at": end, "location": location, "notes": notes, "order_id": orderID, "status": status})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) createAppointment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title    string `json:"title"`
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
		Location string `json:"location"`
		Notes    string `json:"notes"`
		OrderID  int64  `json:"order_id"`
	}
	if !readJSON(w, r, &input) || input.Title == "" || input.StartsAt == "" || input.EndsAt == "" {
		if input.Title == "" || input.StartsAt == "" || input.EndsAt == "" {
			writeError(w, http.StatusBadRequest, "title, start and end are required")
		}
		return
	}
	start, startErr := time.Parse(time.RFC3339, input.StartsAt)
	end, endErr := time.Parse(time.RFC3339, input.EndsAt)
	if startErr != nil || endErr != nil || !end.After(start) {
		writeError(w, 400, "invalid appointment interval")
		return
	}
	u := r.Context().Value(userKey).(user)
	startUTC, endUTC := start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)
	var conflicts int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM appointments WHERE status!='cancelled' AND starts_at < ? AND ends_at > ?", endUTC, startUTC).Scan(&conflicts); err != nil {
		writeError(w, 500, "could not check calendar conflicts")
		return
	}
	if conflicts > 0 {
		writeError(w, 409, "appointment conflicts with an existing event")
		return
	}
	res, err := s.db.Exec("INSERT INTO appointments(title,starts_at,ends_at,location,notes,order_id,created_at,created_by) VALUES(?,?,?,?,?,?,?,?)", input.Title, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), input.Location, input.Notes, nullableID(input.OrderID), time.Now().UTC().Format(time.RFC3339), u.ID)
	if err != nil {
		writeError(w, 500, "could not create appointment")
		return
	}
	id, _ := res.LastInsertId()
	s.audit(u, "create", "appointment", id, map[string]any{"starts_at": input.StartsAt, "ends_at": input.EndsAt})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) cancelAppointment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, 400, "invalid appointment id")
		return
	}
	result, err := s.db.Exec("UPDATE appointments SET status='cancelled' WHERE id=? AND status!='cancelled'", id)
	if err != nil {
		writeError(w, 500, "could not cancel appointment")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, 404, "appointment not found")
		return
	}
	s.audit(r.Context().Value(userKey).(user), "cancel", "appointment", id, nil)
	writeJSON(w, 200, map[string]any{"id": id, "status": "cancelled"})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id,email,name,role,active,created_at FROM users ORDER BY id")
	if err != nil {
		writeError(w, 500, "could not list users")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var email, name, role, created string
		var active int
		if rows.Scan(&id, &email, &name, &role, &active, &created) == nil {
			items = append(items, map[string]any{"id": id, "email": email, "name": name, "role": role, "active": active == 1, "created_at": created})
		}
	}
	writeJSON(w, 200, items)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !readJSON(w, r, &input) || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Email) == "" || len(input.Password) < 12 {
		writeError(w, 400, "name, email and a 12 character password are required")
		return
	}
	if input.Role != "manager" && input.Role != "admin" {
		writeError(w, 400, "invalid role")
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		writeError(w, 500, "could not secure password")
		return
	}
	result, err := s.db.Exec("INSERT INTO users(email,name,password_hash,role,created_at) VALUES(?,?,?,?,?)", strings.ToLower(strings.TrimSpace(input.Email)), strings.TrimSpace(input.Name), hash, input.Role, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		writeError(w, 409, "email already exists")
		return
	}
	id, _ := result.LastInsertId()
	s.audit(r.Context().Value(userKey).(user), "create", "user", id, map[string]any{"role": input.Role})
	writeJSON(w, 201, map[string]any{"id": id, "role": input.Role})
}
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT a.id,a.action,a.entity,a.entity_id,a.details,a.created_at,COALESCE(u.email,'system') FROM audit_events a LEFT JOIN users u ON u.id=a.user_id ORDER BY a.id DESC LIMIT 100")
	if err != nil {
		writeError(w, 500, "could not list audit events")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, entityID int64
		var action, entity, details, created, email string
		if rows.Scan(&id, &action, &entity, &entityID, &details, &created, &email) == nil {
			items = append(items, map[string]any{"id": id, "action": action, "entity": entity, "entity_id": entityID, "details": details, "created_at": created, "user": email})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) audit(u user, action, entity string, entityID int64, details map[string]any) {
	payload, _ := json.Marshal(details)
	if _, err := s.db.Exec("INSERT INTO audit_events(user_id,action,entity,entity_id,details,created_at) VALUES(?,?,?,?,?,?)", u.ID, action, entity, entityID, string(payload), time.Now().UTC().Format(time.RFC3339)); err != nil {
		log.Printf("audit_write_failed entity=%s id=%d error=%v", entity, entityID, err)
	}
}

func (s *Server) allowLogin(key string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	window, ok := s.loginFailures[key]
	if !ok || now.After(window.resetAt) {
		window = loginWindow{resetAt: now.Add(15 * time.Minute)}
	}
	if window.count >= 10 {
		return false
	}
	window.count++
	s.loginFailures[key] = window
	return true
}
func (s *Server) resetLogin(key string) {
	s.loginMu.Lock()
	delete(s.loginFailures, key)
	s.loginMu.Unlock()
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("argon2id$v=19$m=19456,t=2,p=1$%x$%x", salt, key), nil
}
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 {
		return false
	}
	salt, err := hexDecode(parts[3])
	if err != nil {
		return false
	}
	expected, err := hexDecode(parts[4])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
func hexDecode(v string) ([]byte, error) {
	out := make([]byte, len(v)/2)
	for i := range out {
		n, err := strconv.ParseUint(v[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(n)
	}
	return out, nil
}
func readJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		writeError(w, 400, "invalid request body")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func init() { log.SetFlags(log.LstdFlags | log.LUTC) }
