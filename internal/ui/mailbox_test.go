package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestMailboxNeverBlocks reproduces the queue freeze: delivery is stuck (as when the
// event loop is busy in Update), yet posting must return immediately and order is kept.
func TestMailboxNeverBlocks(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	got := make(chan tea.Msg, 10)

	box := newMailbox(func(msg tea.Msg) {
		<-release

		got <- msg
	})

	go box.run()

	t.Cleanup(box.close)

	posted := make(chan struct{})

	go func() {
		for i := range 5 {
			box.post(i)
		}

		close(posted)
	}()

	select {
	case <-posted:
	case <-time.After(time.Second):
		t.Fatal("post blocked while delivery was stuck")
	}

	close(release)

	for i := range 5 {
		require.Equal(t, i, <-got)
	}
}
