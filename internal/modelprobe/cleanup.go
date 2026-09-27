package modelprobe

import "time"

// Cleanup keeps ownership with the worker through bounded teardown. A broken
// closer cannot prevent the caller from receiving already collected evidence.
func Cleanup(close func()) bool {
	done := make(chan bool, 1)
	go func() {
		ok := false
		defer func() { _ = recover(); done <- ok }()
		close()
		ok = true
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case ok := <-done:
		return ok
	case <-timer.C:
		return false
	}
}
