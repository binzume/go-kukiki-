package main

import (
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
	file, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(file)
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
	directory := s.base
	if path != "" && path != "/" {
		resolved, err := s.resolve(path)
		if err != nil {
			return nil
		}
		directory = resolved
	}

	entries, err := os.ReadDir(directory)
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

func (s *LocalStorage) resolve(path string) (string, error) {
	path, err := normalizeStoragePath(path)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.base, path), nil
}
