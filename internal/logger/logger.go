package logger

import (
	"io"
	"log/slog"
	"os"
)

func Setup(logFile string) *slog.Logger {
	var w io.Writer
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			slog.Error("failed to open log file, falling back to stderr", "path", logFile, "err", err)
			w = os.Stderr
		} else {
			w = io.MultiWriter(os.Stderr, f)
		}
	} else {
		w = os.Stderr
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
