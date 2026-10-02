// Package env tells production from development (APP_ENV) and sets up
// logging to match.
package env

import (
	"log/slog"
	"os"
	"strings"
)

// Var is the variable naming the environment: "production" or
// "development".
const Var = "APP_ENV"

// Production reports whether APP_ENV is "production" ("prod" also counts).
// def is the answer when APP_ENV is unset.
func Production(def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(Var))) {
	case "production", "prod":
		return true
	case "":
		return def
	}
	return false
}

// Name is "production" or "development".
func Name(prod bool) string {
	if prod {
		return "production"
	}
	return "development"
}

// Logger logs JSON at info level in production, for log collectors such as
// Vercel's, and readable text in development, at debug level when debug.
func Logger(prod, debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	opt := &slog.HandlerOptions{Level: level}
	if prod {
		return slog.New(slog.NewJSONHandler(os.Stderr, opt))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opt))
}
