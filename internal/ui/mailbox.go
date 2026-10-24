package ui

import (
	"sync"

	tea "charm.land/bubbletea/v2"
)

// mailbox delivers messages to the program in order without ever blocking the
// sender. tea.Program.Send waits until the event loop takes the message, so a send
// from inside Update (e.g. a job queue event raised by enqueueing a job) would
// deadlock the UI.
type mailbox struct {
	deliver func(tea.Msg)

	mu      sync.Mutex
	pending []tea.Msg
	wake    chan struct{}
	done    chan struct{}
}

func newMailbox(deliver func(tea.Msg)) *mailbox {
	return &mailbox{deliver: deliver, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

// post queues msg; it never blocks.
func (b *mailbox) post(msg tea.Msg) {
	b.mu.Lock()
	b.pending = append(b.pending, msg)
	b.mu.Unlock()

	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// run forwards queued messages until close is called.
func (b *mailbox) run() {
	for {
		select {
		case <-b.done:
			return
		case <-b.wake:
		}

		b.mu.Lock()
		batch := b.pending
		b.pending = nil
		b.mu.Unlock()

		for _, msg := range batch {
			b.deliver(msg)
		}
	}
}

func (b *mailbox) close() {
	close(b.done)
}
