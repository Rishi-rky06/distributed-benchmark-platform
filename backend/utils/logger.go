package utils

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the platform-wide structured logger (zap SugaredLogger wrapper).
type Logger = zap.SugaredLogger

// NewLogger builds a zap SugaredLogger from a level string.
// Uses JSON encoding in production, console (colored) in development.
func NewLogger(level string) *Logger {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	cfg := zap.Config{
		Level:    zap.NewAtomicLevelAt(zapLevel),
		Encoding: "json",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	if zapLevel == zapcore.DebugLevel {
		cfg.Encoding = "console"
		cfg.Development = true
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	base, err := cfg.Build()
	if err != nil {
		panic("failed to build logger: " + err.Error())
	}

	return base.Sugar()
}
