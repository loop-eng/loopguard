package watcher

import (
	"sync"
	"time"
)

type Debouncer struct {
	mu       sync.Mutex
	interval time.Duration
	timers   map[string]*time.Timer
}

func NewDebouncer(interval time.Duration) *Debouncer {
	return &Debouncer{
		interval: interval,
		timers:   make(map[string]*time.Timer),
	}
}

func (d *Debouncer) Trigger(key string, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if t, ok := d.timers[key]; ok {
		t.Stop()
	}

	d.timers[key] = time.AfterFunc(d.interval, func() {
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
		fn()
	})
}

func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, t := range d.timers {
		t.Stop()
		delete(d.timers, key)
	}
}
