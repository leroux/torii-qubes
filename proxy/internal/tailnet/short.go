package tailnet

import "time"

func afterShort() <-chan time.Time { return time.After(10 * time.Millisecond) }
