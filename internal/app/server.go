package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	AdminEmail    string
	AdminPassword string
}

type Server struct {
	Port         string
	db           *sql.DB
	secret       []byte
	cookieSecure bool
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
	s := &Server{Port: cfg.Port, db: db, secret: []byte(cfg.JWTSecret), cookieSecure: cfg.CookieSecure}
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
CREATE TABLE IF NOT EXISTS service_orders (id INTEGER PRIMARY KEY AUTOINCREMENT, number TEXT NOT NULL UNIQUE, customer_name TEXT NOT NULL, customer_phone TEXT, service_type TEXT NOT NULL, description TEXT, total_cents INTEGER NOT NULL CHECK(total_cents >= 0), status TEXT NOT NULL DEFAULT 'open', due_date TEXT, created_at TEXT NOT NULL, created_by INTEGER NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS receivables (id INTEGER PRIMARY KEY AUTOINCREMENT, order_id INTEGER NOT NULL REFERENCES service_orders(id), installment_number INTEGER NOT NULL, total_installments INTEGER NOT NULL, due_date TEXT NOT NULL, amount_cents INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'pending');
CREATE TABLE IF NOT EXISTS payments (id INTEGER PRIMARY KEY AUTOINCREMENT, order_id INTEGER REFERENCES service_orders(id), receivable_id INTEGER REFERENCES receivables(id), amount_cents INTEGER NOT NULL CHECK(amount_cents > 0), paid_at TEXT NOT NULL, method TEXT NOT NULL, note TEXT);
CREATE TABLE IF NOT EXISTS expenses (id INTEGER PRIMARY KEY AUTOINCREMENT, description TEXT NOT NULL, category TEXT NOT NULL, amount_cents INTEGER NOT NULL CHECK(amount_cents > 0), due_date TEXT, paid_at TEXT, method TEXT, status TEXT NOT NULL DEFAULT 'pending', created_at TEXT NOT NULL, created_by INTEGER NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS appointments (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, starts_at TEXT NOT NULL, ends_at TEXT NOT NULL, location TEXT, notes TEXT, order_id INTEGER REFERENCES service_orders(id), status TEXT NOT NULL DEFAULT 'scheduled', created_at TEXT NOT NULL, created_by INTEGER NOT NULL REFERENCES users(id));
CREATE TABLE IF NOT EXISTS audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER REFERENCES users(id), action TEXT NOT NULL, entity TEXT NOT NULL, entity_id INTEGER, details TEXT, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_payments_paid_at ON payments(paid_at);
CREATE INDEX IF NOT EXISTS idx_expenses_status ON expenses(status);
CREATE INDEX IF NOT EXISTS idx_appointments_starts_at ON appointments(starts_at);`)
	return err
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
	mux.Handle("POST /api/orders", s.auth(http.HandlerFunc(s.createOrder)))
	mux.Handle("GET /api/expenses", s.auth(http.HandlerFunc(s.listExpenses)))
	mux.Handle("POST /api/expenses", s.auth(http.HandlerFunc(s.createExpense)))
	mux.Handle("GET /api/appointments", s.auth(http.HandlerFunc(s.listAppointments)))
	mux.Handle("POST /api/appointments", s.auth(http.HandlerFunc(s.createAppointment)))
	return s.withHeaders(s.withCORS(s.withStatic(mux)))
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
		if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "emotion"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
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
	s.db.QueryRow("SELECT COALESCE(SUM(r.amount_cents - COALESCE((SELECT SUM(p.amount_cents) FROM payments p WHERE p.receivable_id=r.id),0)),0) FROM receivables r WHERE r.status='pending'").Scan(&receivable)
	s.db.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM expenses WHERE status='pending'").Scan(&payable)
	writeJSON(w, http.StatusOK, map[string]any{"month": month, "received_cents": received, "expenses_cents": expenses, "result_cents": received - expenses, "receivable_cents": receivable, "payable_cents": payable})
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id,number,customer_name,service_type,total_cents,status,due_date,created_at FROM service_orders ORDER BY id DESC LIMIT 100")
	if err != nil {
		writeError(w, 500, "could not list orders")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var number, name, typ, status, due, created string
		var total int64
		if rows.Scan(&id, &number, &name, &typ, &total, &status, &due, &created) == nil {
			items = append(items, map[string]any{"id": id, "number": number, "customer_name": name, "service_type": typ, "total_cents": total, "status": status, "due_date": due, "created_at": created})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CustomerName        string `json:"customer_name"`
		CustomerPhone       string `json:"customer_phone"`
		ServiceType         string `json:"service_type"`
		Description         string `json:"description"`
		DueDate             string `json:"due_date"`
		TotalCents          int64  `json:"total_cents"`
		Installments        int    `json:"installments"`
		FirstDueDate        string `json:"first_due_date"`
		PaymentMethod       string `json:"payment_method"`
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
	if err = tx.QueryRow("INSERT INTO service_orders(number,customer_name,customer_phone,service_type,description,total_cents,status,due_date,created_at,created_by) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING id", fmt.Sprintf("OS-%s-%d", now.Format("20060102"), now.UnixNano()%100000), input.CustomerName, input.CustomerPhone, input.ServiceType, input.Description, input.TotalCents, "open", input.DueDate, now.Format(time.RFC3339), u.ID).Scan(&id); err != nil {
		writeError(w, 500, "could not create order")
		return
	}
	base := input.TotalCents / int64(input.Installments)
	remainder := input.TotalCents % int64(input.Installments)
	due, _ := time.Parse("2006-01-02", input.FirstDueDate)
	if input.FirstDueDate == "" {
		due = now
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
	if input.InitialPaymentCents > 0 {
		remaining := input.InitialPaymentCents
		rows, queryErr := tx.Query("SELECT id, amount_cents FROM receivables WHERE order_id = ? ORDER BY installment_number", id)
		if queryErr != nil {
			writeError(w, 500, "could not allocate payment")
			return
		}
		for rows.Next() {
			var receivableID, amount int64
			if scanErr := rows.Scan(&receivableID, &amount); scanErr != nil {
				rows.Close()
				writeError(w, 500, "could not allocate payment")
				return
			}
			if remaining == 0 {
				break
			}
			allocation := amount
			if remaining < allocation {
				allocation = remaining
			}
			if _, err = tx.Exec("INSERT INTO payments(order_id,receivable_id,amount_cents,paid_at,method) VALUES(?,?,?,?,?)", id, receivableID, allocation, now.Format(time.RFC3339), input.PaymentMethod); err != nil {
				rows.Close()
				writeError(w, 500, "could not create payment")
				return
			}
			remaining -= allocation
		}
		rows.Close()
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "could not save order")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "number": fmt.Sprintf("OS-%s-%d", now.Format("20060102"), now.UnixNano()%100000)})
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
	writeJSON(w, 201, map[string]any{"id": id, "status": status})
}

func (s *Server) listAppointments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id,title,starts_at,ends_at,location,notes,status FROM appointments WHERE starts_at >= ? ORDER BY starts_at LIMIT 100", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		writeError(w, 500, "could not list appointments")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var title, start, end, location, notes, status string
		if rows.Scan(&id, &title, &start, &end, &location, &notes, &status) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "starts_at": start, "ends_at": end, "location": location, "notes": notes, "status": status})
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
	}
	if !readJSON(w, r, &input) || input.Title == "" || input.StartsAt == "" || input.EndsAt == "" {
		if input.Title == "" || input.StartsAt == "" || input.EndsAt == "" {
			writeError(w, http.StatusBadRequest, "title, start and end are required")
		}
		return
	}
	u := r.Context().Value(userKey).(user)
	res, err := s.db.Exec("INSERT INTO appointments(title,starts_at,ends_at,location,notes,created_at,created_by) VALUES(?,?,?,?,?,?,?)", input.Title, input.StartsAt, input.EndsAt, input.Location, input.Notes, time.Now().UTC().Format(time.RFC3339), u.ID)
	if err != nil {
		writeError(w, 500, "could not create appointment")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id})
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
