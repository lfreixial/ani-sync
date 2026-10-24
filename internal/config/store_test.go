package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateAtomicStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "ani-sync")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write("credentials.json", map[string]string{"token": "secret"}); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"": 0700, "credentials.json": 0600} {
		st, err := os.Stat(filepath.Join(path, name))
		if err != nil || st.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", name, st, err)
		}
	}
	var got map[string]string
	if err := s.Read("credentials.json", &got); err != nil || got["token"] != "secret" {
		t.Fatalf("%v %v", got, err)
	}
	if err := s.Write("credentials.json", map[string]string{"token": "new"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Read("../escape", &got); err == nil {
		t.Fatal("traversal accepted")
	}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(); err == nil {
		t.Fatal("concurrent lock acquired")
	}
	unlock()
	if err := s.Remove("credentials.json"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnsafeStorage(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permissions", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "data")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			target := filepath.Join(base, "target")
			if err := os.WriteFile(target, []byte(`{"safe":true}`), 0600); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(path, "credentials.json")
			switch kind {
			case "symlink":
				err = os.Symlink(target, file)
			case "hardlink":
				err = os.Link(target, file)
			case "permissions":
				err = os.WriteFile(file, []byte(`{}`), 0644)
			case "fifo":
				err = makeFIFO(file)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Write("credentials.json", map[string]bool{"unsafe": true}); err == nil {
				t.Fatal("unsafe destination accepted")
			}
			var value any
			if err := s.Read("credentials.json", &value); err == nil {
				t.Fatal("unsafe read accepted")
			}
			b, _ := os.ReadFile(target)
			if string(b) != `{"safe":true}` {
				t.Fatal("target modified")
			}
		})
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(alias, "ani-sync")); err == nil {
		t.Fatal("directory symlink accepted")
	}
}

func TestConfigLoadingAndXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := Load(s)
	if err != nil || c.TitleLanguage != "english" {
		t.Fatal(c, err)
	}
	if err := s.Write("config.json", Config{ClientID: "123", TitleLanguage: "native"}); err != nil {
		t.Fatal(err)
	}
	c, err = Load(s)
	if err != nil || c.ClientID != "123" {
		t.Fatal(c, err)
	}
	for _, c := range []Config{{"-1", "english"}, {"abc", "native"}, {"123", "invalid"}} {
		if c.Validate() == nil {
			t.Fatal(c)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := DefaultPath(); err == nil {
		t.Fatal("relative XDG path")
	}
}
