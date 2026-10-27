package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type LocalStorage struct {
	base     string
	writable bool
}

func NewLoacalStorage(path string) *LocalStorage {
	return &LocalStorage{base: path, writable: true}
}
func (s *LocalStorage) Get(path string) ([]byte, error) {
	name, err := normalizeFSPath(path)
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(s, name)
}
func (s *LocalStorage) Store(path string, data []byte) error {
	file, err := s.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0644)
}
func (s *LocalStorage) Exists(path string) bool {
	file, err := s.resolve(path)
	if err != nil {
		return false
	}
	_, err = os.Stat(file)
	return err == nil
}
func (s *LocalStorage) List(path string) []string {
	name := "."
	if path != "" && path != "/" {
		var err error
		name, err = normalizeFSPath(path)
		if err != nil {
			return nil
		}
	}

	entries, err := fs.ReadDir(s, name)
	if err != nil {
		return nil
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			paths = append(paths, entry.Name())
		}
	}
	return paths
}
func (s *LocalStorage) FilePath(path string) (fpath string, ok bool) {
	file, err := s.resolve(path)
	if err != nil {
		return "", false
	}
	return file, true
}

func (s *LocalStorage) Open(name string) (fs.File, error) {
	name, err := normalizeFSPath(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return os.Open(filepath.Join(s.base, filepath.FromSlash(name)))
}

func (s *LocalStorage) OpenWriter(name string, flag int) (io.WriteCloser, error) {
	name, err := normalizeFSPath(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	path := filepath.Join(s.base, filepath.FromSlash(name))
	if flag&os.O_CREATE != 0 {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, flag, 0644)
}

func (s *LocalStorage) Rename(oldName, newName string) error {
	oldName, err := normalizeFSPath(oldName)
	if err != nil {
		return err
	}
	newName, err = normalizeFSPath(newName)
	if err != nil {
		return err
	}
	return os.Rename(
		filepath.Join(s.base, filepath.FromSlash(oldName)),
		filepath.Join(s.base, filepath.FromSlash(newName)),
	)
}

func (s *LocalStorage) Remove(name string) error {
	name, err := normalizeFSPath(name)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(s.base, filepath.FromSlash(name)))
}

func (s *LocalStorage) resolve(path string) (string, error) {
	path, err := normalizeStoragePath(path)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.base, path), nil
}
