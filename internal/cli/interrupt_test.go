package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// R1: Ctrl-C at a prompt aborts at once (exit 10, terminal restored); elsewhere it only
// cancels the run's context.
func TestHandleInterrupt_AbortsAtPromptRestoringTerminal(t *testing.T) {
	restored, exited := false, -1
	end := beginPrompt(func() { restored = true })
	var errb bytes.Buffer
	cancelled := false
	handleInterrupt(&errb, func(c int) { exited = c }, func() { cancelled = true })
	if exited != 10 || !restored || cancelled || !strings.Contains(errb.String(), "aborted, nothing was sent") {
		t.Errorf("exit %d restored %v cancelled %v out %q", exited, restored, cancelled, errb.String())
	}
	end()
	// Prompt over: the next Ctrl-C only cancels.
	exited, restored = -1, false
	handleInterrupt(&errb, func(c int) { exited = c }, func() { cancelled = true })
	if exited != -1 || restored || !cancelled {
		t.Errorf("exit %d restored %v cancelled %v", exited, restored, cancelled)
	}
}

func TestPrompter_InjectedConfirmCountsAsPrompt(t *testing.T) {
	s := &session{deps: Deps{Confirm: func(string) (string, error) {
		if !interruptPromptPeek() {
			t.Error("no prompt registered while the confirmation is waiting")
		}
		return "y", nil
	}}}
	if _, err := s.confirmer()("q"); err != nil {
		t.Fatal(err)
	}
	if interruptPromptPeek() {
		t.Error("prompt still registered after the answer")
	}
	_ = context.Background
}

func interruptPromptPeek() bool {
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	return prompts.active > 0
}
