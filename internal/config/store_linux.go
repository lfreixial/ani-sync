// Package config provides private, descriptor-relative Linux storage.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxFile = 4 << 20

type Store struct {
	dir  *os.File
	Path string
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("XDG_CONFIG_HOME must be absolute")
	}
	return filepath.Join(base, "ani-sync"), nil
}

// Open walks each directory with O_NOFOLLOW. No path component may be a symlink.
func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == "/" {
		return nil, errors.New("configuration path must be an absolute directory")
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	for i, part := range parts {
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if errors.Is(e, syscall.ENOENT) {
			e = syscall.Mkdirat(fd, part, 0700)
			if e == nil || errors.Is(e, syscall.EEXIST) {
				next, e = syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
			}
		}
		syscall.Close(fd)
		if e != nil {
			return nil, fmt.Errorf("open private configuration directory: %w", e)
		}
		fd = next
		if i == len(parts)-1 {
			var st syscall.Stat_t
			if e = syscall.Fstat(fd, &st); e == nil && st.Uid != uint32(os.Getuid()) {
				e = errors.New("configuration directory belongs to another user")
			}
			if e == nil {
				e = syscall.Fchmod(fd, 0700)
			}
			if e != nil {
				syscall.Close(fd)
				return nil, e
			}
		}
	}
	return &Store{dir: os.NewFile(uintptr(fd), path), Path: filepath.Clean(path)}, nil
}

func (s *Store) Close() error { return s.dir.Close() }
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

func (s *Store) open(name string, flags int) (*os.File, error) {
	if !validName(name) {
		return nil, errors.New("invalid storage filename")
	}
	fd, err := syscall.Openat(int(s.dir.Fd()), name, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := f.Stat()
	if err == nil {
		sys := st.Sys().(*syscall.Stat_t)
		if !st.Mode().IsRegular() || sys.Uid != uint32(os.Getuid()) || sys.Nlink != 1 || st.Mode().Perm() != 0600 {
			err = errors.New("storage file must be owned by you, regular, unlinked elsewhere, and mode 0600")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (s *Store) Read(name string, dst any) error {
	f, err := s.open(name, syscall.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return err
	}
	if len(b) > maxFile {
		return errors.New("storage file too large")
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return errors.New("invalid JSON in " + name)
	}
	return nil
}

func (s *Store) Write(name string, value any) error {
	if !validName(name) {
		return errors.New("invalid storage filename")
	}
	// Reject suspicious existing destinations; rename never follows the destination.
	if f, err := s.open(name, syscall.O_RDONLY); err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("cannot encode storage value")
	}
	if len(b) > maxFile {
		return errors.New("storage value too large")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := ".tmp-" + hex.EncodeToString(nonce[:])
	f, err := s.open(tmp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err != nil {
		return err
	}
	defer s.Remove(tmp)
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := syscall.Renameat(int(s.dir.Fd()), tmp, int(s.dir.Fd()), name); err != nil {
		return err
	}
	return s.dir.Sync()
}

func (s *Store) Remove(name string) error {
	if !validName(name) {
		return errors.New("invalid storage filename")
	}
	err := syscall.Unlinkat(int(s.dir.Fd()), name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.dir.Sync()
}

// Lock serializes local commands. The descriptor is never inherited by children.
func (s *Store) Lock() (func(), error) {
	f, err := s.open("lock", syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another ani-sync command is running; finish or cancel it in its original terminal (check 'jobs -l' for a suspended command)")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
