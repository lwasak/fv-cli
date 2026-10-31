package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"

	"github.com/lwasak/fv-cli/internal/exitcode"
)

// Ctrl-C has two meanings in fv. While fv waits for the user at a prompt (the PRODUCTION
// confirmation, the fv init questions, the key passphrase) nothing has been sent, so the
// process stops at once with exit code 10 ("aborted") and a terminal switched to no-echo for
// the passphrase gets its echo back. Everywhere else the run's context is cancelled, so the
// send, status, lookup and lock-wait code stops through the normal error paths and an invoice
// in flight ends as sending or unknown, never half-recorded.
var prompts struct {
	mu      sync.Mutex
	active  int
	restore []func()
}

// beginPrompt marks an interactive read as running. restore (may be nil) undoes terminal
// changes if the process has to stop in the middle of it. The returned func ends the read.
func beginPrompt(restore func()) (end func()) {
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	prompts.active++
	if restore != nil {
		prompts.restore = append(prompts.restore, restore)
	}
	return func() {
		prompts.mu.Lock()
		defer prompts.mu.Unlock()
		prompts.active--
		if restore != nil && len(prompts.restore) > 0 {
			prompts.restore = prompts.restore[:len(prompts.restore)-1]
		}
	}
}

// interruptPrompt handles an interrupt that arrives while a prompt may be open. It reports
// whether one was open; if so it has restored the terminal.
func interruptPrompt() bool {
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	if prompts.active == 0 {
		return false
	}
	for i := len(prompts.restore) - 1; i >= 0; i-- {
		prompts.restore[i]()
	}
	return true
}

// handleInterrupt is the reaction to the first Ctrl-C: abort at a prompt, else cancel the run.
func handleInterrupt(stderr io.Writer, exit func(int), cancel context.CancelFunc) {
	if interruptPrompt() {
		_, _ = fmt.Fprintln(stderr, "\naborted, nothing was sent")
		exit(exitcode.CodeAborted)
		return
	}
	cancel()
}

// watchInterrupt installs the Ctrl-C handler for the real process and returns the run's
// context. After the first interrupt the default behaviour returns, so a second Ctrl-C kills
// fv.
func watchInterrupt(stderr io.Writer) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		select {
		case <-ch:
			signal.Stop(ch)
			handleInterrupt(stderr, os.Exit, cancel)
		case <-ctx.Done():
		}
	}()
	return ctx, func() { signal.Stop(ch); cancel() }
}
