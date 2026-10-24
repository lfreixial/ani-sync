package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "ani-cli":
		if len(os.Args) > 1 && os.Args[1] == "--help" {
			fmt.Fprintln(os.Stdout, "--episode --no-detach --exit-after-play")
		} else {
			_ = json.NewEncoder(os.Stdout).Encode(struct{ Args, Env []string }{os.Args[1:], os.Environ()})
		}
		os.Exit(0)
	case "ani-cli-old":
		fmt.Fprintln(os.Stdout, "--episode")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLaunchWithFakeInstalledPrograms(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ani-cli", "ani-cli-old", "mpv"} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	withMPRISPlugins(t, nil)
	t.Setenv("ANILIST_TOKEN", "do-not-forward")
	t.Setenv("ANI_CLI_PLAYER_FLAGS", "bad-flags")
	var out bytes.Buffer
	if err := Launch(context.Background(), "Sousou", 3, Streams{Out: &out, Err: io.Discard}); err != nil {
		t.Fatal(err)
	}
	var report struct{ Args, Env []string }
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Args[len(report.Args)-1] != "Sousou" {
		t.Fatal(report.Args)
	}
	playerFlags := 0
	for _, e := range report.Env {
		if strings.HasPrefix(e, "ANI_CLI_PLAYER_FLAGS=") {
			playerFlags++
			if e != "ANI_CLI_PLAYER_FLAGS=--no-config --demuxer=lavf --terminal=yes" {
				t.Fatalf("unexpected player flags: %q", e)
			}
		}
		if strings.Contains(e, "do-not-forward") || strings.Contains(e, "bad-flags") {
			t.Fatal("unsafe environment")
		}
	}
	if playerFlags != 1 {
		t.Fatalf("want exactly one player flags setting, got %d", playerFlags)
	}
	if err := Check(context.Background(), filepath.Join(dir, "ani-cli-old")); err == nil {
		t.Fatal("unsupported flags accepted")
	}
}

func TestArguments(t *testing.T) {
	query := "Frieren; $(touch /nope) `id` --update"
	got, err := Arguments(query, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--episode", "3", "--no-detach", "--exit-after-play", query}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	for _, q := range []string{"--update", "-U", "a\n--update"} {
		if _, err := Arguments(q, 1); err == nil {
			t.Fatal("option injection accepted")
		}
	}
	if _, err := Arguments("Frieren", 0); err == nil {
		t.Fatal("episode zero accepted")
	}
}

// The test binary is the fake subprocess. No shell or streaming site is used.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("ANI_SYNC_TEST_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) > 0 && args[0] == "wait" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if len(args) > 0 && args[0] == "fail" {
		os.Exit(7)
	}
	_ = json.NewEncoder(os.Stdout).Encode(args)
	os.Exit(0)
}

func TestSubprocessSafetyAndCancellation(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(Environment(), "ANI_SYNC_TEST_HELPER=1")
	arg := "title;$(echo unsafe) --update"
	out, err := Capture(context.Background(), exe, []string{"-test.run=^TestHelperProcess$", "--", arg}, env, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal([]byte(out), &args); err != nil || len(args) != 1 || args[0] != arg {
		t.Fatal(args, err)
	}
	if err := Run(context.Background(), exe, []string{"-test.run=^TestHelperProcess$", "--", "fail"}, env, Streams{}); err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = Run(ctx, exe, []string{"-test.run=^TestHelperProcess$", "--", "wait"}, env, Streams{})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatal(err)
	}
}

func TestUnsafeExecutableAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe")
	if err := os.WriteFile(path, []byte("not executable content"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := TrustedPath(path); err == nil {
		t.Fatal("writable executable accepted")
	}
	t.Setenv("ANILIST_TOKEN", "secret")
	t.Setenv("ANI_CLI_PLAYER", "evil")
	t.Setenv("FZF_DEFAULT_OPTS", "--bind=start:execute(evil)")
	t.Setenv("LD_PRELOAD", "evil")
	t.Setenv("ENV", "evil")
	for _, v := range Environment() {
		for _, prefix := range []string{"ANILIST_", "ANI_CLI_", "FZF_", "LD_", "ENV="} {
			if strings.HasPrefix(v, prefix) {
				t.Fatal(v)
			}
		}
	}
}

func withMPRISPlugins(t *testing.T, paths []string) {
	old := mprisPlugins
	mprisPlugins = paths
	t.Cleanup(func() { mprisPlugins = old })
}

func TestMPRISPlugin(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "mpris.so")
	if err := os.WriteFile(plugin, []byte("so"), 0644); err != nil {
		t.Fatal(err)
	}
	spaced := filepath.Join(dir, "with space.so")
	if err := os.WriteFile(spaced, []byte("so"), 0644); err != nil {
		t.Fatal(err)
	}
	writable := filepath.Join(dir, "writable.so")
	if err := os.WriteFile(writable, []byte("so"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(writable, 0666); err != nil {
		t.Fatal(err)
	}

	withMPRISPlugins(t, []string{filepath.Join(dir, "missing.so"), spaced, writable, plugin})
	if got := MPRISPlugin(); got != plugin {
		t.Fatalf("want %q, got %q", plugin, got)
	}
	withMPRISPlugins(t, []string{spaced, writable})
	if got := MPRISPlugin(); got != "" {
		t.Fatalf("unsafe plugin accepted: %q", got)
	}
}
