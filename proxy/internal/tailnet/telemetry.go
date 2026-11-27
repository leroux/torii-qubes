package tailnet

import (
	"sync"

	"tailscale.com/envknob"
	"tailscale.com/logtail"
)

var (
	telemetryMu sync.Mutex
	logsOff     bool // log uploads turned off in this process (for good)
)

// SetTelemetry decides what tsnet may send on its own, outside the
// identities' traffic and over the proxy qube's own network, for every
// identity in this process. Call it before identities start.
//
// The port mapper (UPnP, NAT-PMP, PCP) is always off: a proxy qube sits
// behind Qubes' own NAT and never reaches a home router, so all it does
// is broadcast. Client log uploads to Tailscale (log.tailscale.com) are
// off unless logs; off also tells the control server so. Once off, they
// stay off for the process: it reports true when logs are wanted again,
// and the program must restart for that.
//
//charter:realizes SW-261127-8PTP
func SetTelemetry(logs bool) (restart bool) {
	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	envknob.Setenv("TS_DISABLE_PORTMAPPER", "true")
	if logs {
		return logsOff
	}
	logtail.Disable()
	envknob.SetNoLogsNoSupport()
	logsOff = true
	return false
}
