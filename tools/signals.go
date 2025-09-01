package tools

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func onQuitSignals(quit <-chan struct{}, notify func(error)) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case sig := <-sigs:
		notify(signalMessage(sig))
	case <-quit:
	}
	signal.Stop(sigs)
	close(sigs)
}

func signalMessage(sig os.Signal) error {
	switch sig {
	case os.Interrupt:
		return errors.New("Received keyboard interrupt")
	case os.Kill:
		return errors.New("Received kill signal")
	case syscall.SIGTERM:
		return errors.New("Received term signal")
	case syscall.SIGHUP:
		return errors.New("Received hang up signal (parent process died)")
	default:
		return fmt.Errorf("Recieved %v", sig)
	}
}

// Closes the given channel when the OS sends a signal to stop.
// Also logs which signal was received
//
// Catches: SIGINT, SIGKILL, SIGTERM, SIGHUP
func CloseOnQuitSignals(quit chan struct{}) {
	go onQuitSignals(quit, func(err error) {
		log.Print(err)
		close(quit)
	})
}

// Returns a new context that will be cancelled with a cause when the OS sends
// a signal to stop.
//
// Catches: SIGINT, SIGKILL, SIGTERM, SIGHUP
//
// Canceling this context releases resources associated with it, so code should
// call cancel as soon as the operations running in this Context complete.
func ContextWithQuitSignals(ctx context.Context) (context.Context, context.CancelCauseFunc) {
	ret, cancel := context.WithCancelCause(ctx)
	go func() {
		onQuitSignals(ret.Done(), cancel)
		cancel(context.Cause(ret))
	}()
	return ret, cancel
}
