package childproc

import (
	"os/exec"
	"testing"
	"time"
)

func TestStartAndReapWaitsForChildExactlyOnce(t *testing.T) {
	command := exec.Command("sh", "-c", "exit 0")
	waitDone := make(chan error, 1)
	waitCalls := 0
	if err := startAndReap(command, func(command *exec.Cmd) error {
		waitCalls++
		err := command.Wait()
		waitDone <- err
		return err
	}); err != nil {
		t.Fatalf("startAndReap: %v", err)
	}

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("wait child: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child was not reaped")
	}
	if waitCalls != 1 {
		t.Fatalf("Wait calls = %d, want 1", waitCalls)
	}
}

func TestStartAndReapReturnsStartFailure(t *testing.T) {
	command := exec.Command(t.TempDir() + "/missing-executable")
	if err := StartAndReap(command); err == nil {
		t.Fatal("StartAndReap accepted a missing executable")
	}
}
