package main

import (
	"io/ioutil"
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
	return ioutil.ReadFile(s.resolve(path))
}
func (s *LocalStorage) Store(path string, data []byte) error {
	return ioutil.WriteFile(s.resolve(path), data, 0)
}
func (s *LocalStorage) Exists(path string) bool {
	_, err := os.Stat(s.resolve(path))
	return err == nil
}
func (s *LocalStorage) List(path string) []string {
	return nil
}
func (s *LocalStorage) FilePath(path string) (fpath string, ok bool) {
	return s.resolve(path), true
}

func (s *LocalStorage) resolve(path string) string {
	return filepath.Join(s.base, filepath.Clean(path))
}
