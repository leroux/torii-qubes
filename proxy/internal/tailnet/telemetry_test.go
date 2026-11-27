package tailnet

import (
	"github.com/leroux/torii-qubes/claims"
	"os"
	"testing"

	"tailscale.com/envknob"
)

func TestTelemetryOffByDefault(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_8PTP, "logs-off-by-default", "logs-on-asked")
	t.Cleanup(func() { logsOff = false; os.Unsetenv("TS_NO_LOGS_NO_SUPPORT"); os.Unsetenv("TS_DISABLE_PORTMAPPER") })
	if SetTelemetry(false) {
		t.Fatal("turning logs off needs no restart")
	}
	if !envknob.NoLogsNoSupport() {
		t.Error("log uploads still on")
	}
	if !envknob.Bool("TS_DISABLE_PORTMAPPER") {
		t.Error("the port mapper is still on")
	}
	// Off for the life of the process: turning them on needs a restart.
	if !SetTelemetry(true) {
		t.Error("logs on after off should ask for a restart")
	}
	if !envknob.Bool("TS_DISABLE_PORTMAPPER") {
		t.Error("the port mapper is on with logs on")
	}
}

func TestTelemetryOnFromTheStart(t *testing.T) {
	t.Cleanup(func() { logsOff = false; os.Unsetenv("TS_NO_LOGS_NO_SUPPORT"); os.Unsetenv("TS_DISABLE_PORTMAPPER") })
	if SetTelemetry(true) {
		t.Fatal("logs on from the start needs no restart")
	}
	if envknob.NoLogsNoSupport() {
		t.Error("logs on, but no-logs-no-support set")
	}
}
