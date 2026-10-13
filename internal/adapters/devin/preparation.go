package devin

import "time"

// Empty native sessions are in-memory until the first user prompt. Retain the
// creating process across Init -> durable Squad state -> Send, with an expiry
// and explicit worker cleanup. Never silently recreate an expired identity.
type preparation struct {
	c         *client
	cleanup   func()
	session   session
	workspace string
	timer     *time.Timer
	stop      func() bool
}

func (p *preparation) close() error {
	if p.timer != nil {
		p.timer.Stop()
	}
	if p.stop != nil {
		p.stop()
	}
	defer p.cleanup()
	return p.c.close()
}
func (a *Adapter) expirePreparation(p *preparation) {
	a.mu.Lock()
	if a.prepared != p {
		a.mu.Unlock()
		return
	}
	a.prepared = nil
	a.mu.Unlock()
	_ = p.close()
}
func (a *Adapter) ClosePreparation() error {
	a.mu.Lock()
	p := a.prepared
	a.prepared = nil
	a.mu.Unlock()
	if p != nil {
		return p.close()
	}
	return nil
}
