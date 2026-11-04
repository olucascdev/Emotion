package main

import (
	"log"
	"net/http"
	"os"

	"github.com/olucascdev/Emotion/internal/app"
)

func main() {
	server, err := app.NewServer(app.Config{
		Port:          env("PORT", "8080"),
		DatabasePath:  env("DATABASE_PATH", "./data/emotion.db"),
		JWTSecret:     env("JWT_SECRET", "development-only-change-me-please"),
		CookieSecure:  env("COOKIE_SECURE", "false") == "true",
		AdminEmail:    env("ADMIN_EMAIL", "admin@example.com"),
		AdminPassword: env("ADMIN_PASSWORD", "change-me-now"),
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Emotion listening on :%s", server.Port)
	log.Fatal(http.ListenAndServe(":"+server.Port, server.Handler()))
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
