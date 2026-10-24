// Package playback launches validated local programs with explicit arguments.
package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// TrustedPath rejects relative executables and files/directories writable by
// other users. The root filesystem owner is accepted for rootless containers.
func TrustedPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not installed on PATH", filepath.Base(name))
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("executable must resolve to an absolute path")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("cannot resolve executable")
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return "", errors.New("executable is not a regular executable file")
	}
	if err := checkOwnership(path); err != nil {
		return "", err
	}
	return path, nil
}

func checkOwnership(path string) error {
	root, err := os.Stat("/")
	if err != nil {
		return err
	}
	rootUID := root.Sys().(*syscall.Stat_t).Uid
	for p := path; ; p = filepath.Dir(p) {
		st, err := os.Stat(p)
		if err != nil {
			return errors.New("cannot inspect executable path")
		}
		uid := st.Sys().(*syscall.Stat_t).Uid
		stickyRootDirectory := st.IsDir() && st.Mode()&os.ModeSticky != 0 && (uid == 0 || uid == rootUID)
		if (uid != 0 && uid != uint32(os.Getuid()) && uid != rootUID) || (st.Mode().Perm()&0022 != 0 && !stickyRootDirectory) {
			return errors.New("executable path is owned by an untrusted user or writable by others")
		}
		if p == "/" {
			break
		}
	}
	return nil
}

// Environment is deliberately small. No ANI_CLI_*, FZF_*, shell startup hooks,
// loader hooks, proxies, or authentication variables are inherited.
func Environment() []string {
	env := []string{"LANG=C.UTF-8"}
	for _, key := range []string{"HOME", "TERM", "COLORTERM", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "XAUTHORITY", "PULSE_SERVER", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	dirs := []string{}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(p) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil && checkOwnership(resolved) == nil {
			dirs = append(dirs, resolved)
		}
	}
	env = append(env, "PATH="+strings.Join(dirs, string(os.PathListSeparator)))
	return env
}

type Streams struct {
	In       io.Reader
	Out, Err io.Writer
}

func IsTerminal(f *os.File) bool {
	var term syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&term)))
	return err == 0
}

func Run(ctx context.Context, path string, args, env []string, streams Streams) error {
	validated, err := TrustedPath(path)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, validated, args...)
	cmd.Env = env
	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err
	// Keep terminal input working while isolating cancellation of the process tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A separate process group needs the foreground terminal while interacting.
	// Go ignores SIGTTOU, allowing the parent to restore its group after Wait.
	if f, ok := streams.Err.(*os.File); ok && IsTerminal(f) {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return errors.New("cannot open controlling terminal")
		}
		defer tty.Close()
		var previous int32
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&previous)))
		if errno != 0 || int(previous) != syscall.Getpgrp() {
			return errors.New("ani-sync must run in the foreground for interactive playback")
		}
		cmd.SysProcAttr.Foreground = true
		cmd.SysProcAttr.Ctty = int(tty.Fd())
		defer func() {
			syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&previous)))
		}()
	}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("%s exited with status %d", filepath.Base(path), exit.ExitCode())
		}
		return fmt.Errorf("%s could not start", filepath.Base(path))
	}
	return nil
}

type limitBuffer struct {
	data  []byte
	limit int
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > b.limit {
		return 0, errors.New("subprocess output exceeds size limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func Capture(ctx context.Context, path string, args, env []string, input io.Reader, stderr io.Writer) (string, error) {
	b := &limitBuffer{limit: 1 << 20}
	err := Run(ctx, path, args, env, Streams{input, b, stderr})
	return string(b.data), err
}
