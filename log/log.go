// Package log provides utility functions for logging to the console.
package log

import (
	"fmt"

	"github.cbhq.net/nodes/rosetta-flow/process"
	"github.com/rs/zerolog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger *zap.Logger
	sugar  *zap.SugaredLogger
)

// Badger wraps the global zap.Logger for the badger.Logger interface.
type Badger struct {
	Prefix string
}

// Debugf uses fmt.Sprintf to log a formatted string.
func (b Badger) Debugf(format string, args ...interface{}) {
	msg := fmt.Sprintf("["+b.Prefix+"/badger] "+format, args...)
	if msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	sugar.Debugw(msg)
}

// Errorf uses fmt.Sprintf to log a formatted string.
func (b Badger) Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf("["+b.Prefix+"/badger] "+format, args...)
	if msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	sugar.Errorw(msg)
}

// Infof uses fmt.Sprintf to log a formatted string.
func (b Badger) Infof(format string, args ...interface{}) {
	msg := fmt.Sprintf("["+b.Prefix+"/badger] "+format, args...)
	if msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	sugar.Infow(msg)
}

// Warningf uses fmt.Sprintf to log a formatted string.
func (b Badger) Warningf(format string, args ...interface{}) {
	msg := fmt.Sprintf("["+b.Prefix+"/badger] "+format, args...)
	if msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	sugar.Warnw(msg)
}

// Debugf uses fmt.Sprintf to log a formatted string.
func Debugf(format string, args ...interface{}) {
	sugar.Debugf(format, args...)
}

// Error logs an error message with any optional fields.
func Error(msg string, fields ...zap.Field) {
	logger.Error(msg, fields...)
}

// Errorf uses fmt.Sprintf to log a formatted string.
func Errorf(format string, args ...interface{}) {
	sugar.Errorf(format, args...)
}

// Fatalf uses fmt.Sprintf to log a formatted string, and then calls
// process.Exit.
func Fatalf(format string, args ...interface{}) {
	sugar.Errorf(format, args...)
	process.Exit(1)
}

// Info logs an info message with any optional fields.
func Info(msg string, fields ...zap.Field) {
	logger.Info(msg, fields...)
}

// Infof uses fmt.Sprintf to log a formatted string.
func Infof(format string, args ...interface{}) {
	sugar.Infof(format, args...)
}

// Warn logs a warn message with any optional fields.
func Warn(msg string, fields ...zap.Field) {
	logger.Warn(msg, fields...)
}

// Warnf uses fmt.Sprintf to log a formatted string.
func Warnf(format string, args ...interface{}) {
	sugar.Warnf(format, args...)
}

func init() {
	enc := zap.NewDevelopmentEncoderConfig()
	enc.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg := zap.Config{
		DisableCaller:     true,
		DisableStacktrace: true,
		EncoderConfig:     enc,
		Encoding:          "console",
		ErrorOutputPaths:  []string{"stderr"},
		Level:             zap.NewAtomicLevelAt(zap.InfoLevel),
		OutputPaths:       []string{"stderr"},
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},
	}
	logger, _ = cfg.Build()
	sugar = logger.Sugar()
	zap.RedirectStdLog(logger)
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	process.SetExitHandler(func() {
		/* #nosec G104 -- We purposefully ignore any errors here */
		logger.Sync()
	})
}
