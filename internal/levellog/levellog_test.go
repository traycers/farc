package levellog_test

import (
	"fmt"
	"testing"

	"github.com/traycers/farc/internal/levellog"
)

func TestLogger_Info_PrependsLevel(t *testing.T) {
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	levellog.New(logf).Info("farcd: starting (%d storages)", 3)

	if len(logs) != 1 || logs[0] != "level=INFO farcd: starting (3 storages)" {
		t.Fatalf("logs = %v, want [\"level=INFO farcd: starting (3 storages)\"]", logs)
	}
}

func TestLogger_Warn_PrependsLevel(t *testing.T) {
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	levellog.New(logf).Warn("ingest: channel %d: reconnecting", 7)

	if len(logs) != 1 || logs[0] != "level=WARN ingest: channel 7: reconnecting" {
		t.Fatalf("logs = %v, want [\"level=WARN ingest: channel 7: reconnecting\"]", logs)
	}
}

func TestLogger_Error_PrependsLevel(t *testing.T) {
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	levellog.New(logf).Error("farcd: exited with error: %v", "boom")

	if len(logs) != 1 || logs[0] != "level=ERROR farcd: exited with error: boom" {
		t.Fatalf("logs = %v, want [\"level=ERROR farcd: exited with error: boom\"]", logs)
	}
}

func TestLogger_New_NilLogfIsNoop(t *testing.T) {
	l := levellog.New(nil)
	l.Info("should not panic")
	l.Warn("should not panic")
	l.Error("should not panic")
}
