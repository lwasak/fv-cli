package fsafe

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLock_TimeoutError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := Lock(p, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = Lock(p, 150*time.Millisecond)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("err = %v, want ErrLockTimeout", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Errorf("returned too early: %v", time.Since(start))
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("second unlock: %v", err)
	}
	unlock2, err := Lock(p, time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = unlock2()
}

func TestLock_MutualExclusion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	var mu sync.Mutex
	inside := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := Lock(p, 10*time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inside++
			if inside != 1 {
				t.Errorf("%d holders at once", inside)
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			_ = unlock()
		}()
	}
	wg.Wait()
}

// Ctrl-C while waiting for a held lock ends the wait at once, long before the timeout.
func TestLockContext_CancelEndsTheWait(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := Lock(p, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err = LockContext(ctx, p, 30*time.Second)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrLockTimeout) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("waited %v", d)
	}
}
