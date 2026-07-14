// Package logger provides a small wrapper around zerolog so the rest of the
// application depends on a stable logging surface.
package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

// New builds a configured zerolog.Logger.
//
// level is one of trace|debug|info|warn|error. When pretty is true a
// human-friendly console writer is used (development); otherwise structured
// JSON is emitted (production).
func New(level string, pretty bool) zerolog.Logger {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil || level == "" {
		lvl = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = time.RFC3339

	var l zerolog.Logger
	if pretty {
		cw := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "15:04:05"}
		l = zerolog.New(cw)
	} else {
		l = zerolog.New(os.Stdout)
	}
	return l.Level(lvl).With().Timestamp().Str("service", "apexion").Logger()
}
