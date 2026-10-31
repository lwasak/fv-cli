package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode_Mapping(t *testing.T) {
	cause := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain", cause, 0},
		{"registry", ErrRegistry, 2},
		{"rejected", ErrRejected, 4},
		{"unknown", ErrUnknown, 5},
		{"aborted", ErrAborted, 10},
		{"wrapped sentinel", fmt.Errorf("ctx: %w", ErrRejected), 4},
		{"Wrap", Wrap(ErrUnknown, cause), 5},
		{"Wrap then fmt", fmt.Errorf("outer: %w", Wrap(ErrRegistry, cause)), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Errorf("Code = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWrap_KeepsCauseAndText(t *testing.T) {
	cause := errors.New("white list: 429")
	err := Wrap(ErrRegistry, cause)
	if !errors.Is(err, ErrRegistry) || !errors.Is(err, cause) {
		t.Fatalf("Is: sentinel=%v cause=%v", errors.Is(err, ErrRegistry), errors.Is(err, cause))
	}
	if err.Error() != cause.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), cause.Error())
	}
	if Wrap(ErrRegistry, nil) != nil {
		t.Error("Wrap(nil) must be nil")
	}
}
