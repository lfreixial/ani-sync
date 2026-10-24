package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Exercise real terminal ownership without a player, shell, or network access.
func TestTerminalRestoredAfterChild(t *testing.T) {
	for _, mode := range []string{"success", "fail", "wait"} {
		t.Run(mode, func(t *testing.T) {
			master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
			if err != nil {
				t.Skipf("pseudo-terminal unavailable: %v", err)
			}
			defer master.Close()
			var unlock, number uint32
			if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
				t.Fatal(errno)
			}
			if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
				t.Fatal(errno)
			}
			slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer slave.Close()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestTerminalSessionHelper$")
			cmd.Env = append(Environment(), "ANI_SYNC_TERMINAL_TEST="+mode)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			if err := cmd.Run(); err != nil {
				slave.Close()
				output, _ := io.ReadAll(io.LimitReader(master, 8192))
				t.Fatalf("terminal was not restored after %s: %v (timeout: %v)\n%s", mode, err, ctx.Err(), output)
			}
		})
	}
}

func TestTerminalSessionHelper(t *testing.T) {
	mode := os.Getenv("ANI_SYNC_TERMINAL_TEST")
	if mode == "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if mode == "wait" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
	}
	ignored := signal.Ignored(syscall.SIGTTOU)
	err = Run(ctx, exe, []string{"-test.run=^TestHelperProcess$", "--", mode}, append(Environment(), "ANI_SYNC_TEST_HELPER=1"), Streams{os.Stdin, os.Stdout, os.Stderr})
	if (mode == "success" && err != nil) || (mode == "fail" && err == nil) || (mode == "wait" && !errors.Is(err, context.DeadlineExceeded)) {
		t.Fatalf("unexpected child result: %v", err)
	}
	if signal.Ignored(syscall.SIGTTOU) != ignored {
		t.Fatal("SIGTTOU disposition was not restored")
	}
	var foreground int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&foreground))); errno != 0 || int(foreground) != syscall.Getpgrp() {
		t.Fatalf("terminal foreground group %d, want %d (errno: %v)", foreground, syscall.Getpgrp(), errno)
	}
}
