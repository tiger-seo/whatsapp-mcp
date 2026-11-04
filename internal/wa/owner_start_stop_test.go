package wa

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestOwnerStopWaitsForPairingGenerationConstruction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		ot := newOwnerTest(t, false, false, nil)
		var calls int
		ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
			calls++
			if calls == 2 {
				close(entered)
				<-release
			}
			ot.sess.cfg = cfg
			return ot.sess, nil
		}
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		ot.o.retireGeneration(ot.o.gen)
		result := make(chan error, 1)
		go func() {
			_, err := ot.o.RequestPairing(context.Background())
			result <- err
		}()
		<-entered
		ot.o.BeginStop()
		select {
		case <-ot.o.Done():
			t.Error("stop completed before pairing generation construction returned")
		default:
		}
		close(release)
		if err := <-result; !errors.Is(err, ErrStopping) {
			t.Errorf("RequestPairing = %v, want ErrStopping", err)
		}
		<-ot.o.Done()
		if n := ot.sess.retiredN.Load(); n != 2 {
			t.Errorf("retirements = %d, want 2", n)
		}
	})
}
